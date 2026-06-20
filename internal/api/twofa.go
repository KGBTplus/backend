package api

import (
	"encoding/json"
	"net/http"
)

// ---------- 2FA отключение ----------

func (s *Server) Disable2FA(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	if err := s.DB.DisableEmailOTP(r.Context(), userID); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка отключения 2FA")
		return
	}

	key := "2fa_setup:" + userID.String()
	s.rateMu.Lock()
	delete(s.codeRateLimits, key)
	s.rateMu.Unlock()
	s.codesMu.Lock()
	delete(s.codes, key)
	s.codesMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "2FA disabled"})
}

// ---------- 2FA включение ----------

func (s *Server) Setup2FA(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	user, err := s.DB.GetUserByIDWithVerified(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения пользователя")
		return
	}

	if !user.EmailVerified {
		sendError(w, http.StatusForbidden, "Подтвердите email перед включением 2FA")
		return
	}

	if user.EmailOtpEnabled {
		sendError(w, http.StatusConflict, "2FA уже включена")
		return
	}

	if !s.checkCodeSendRateLimit("2fa_setup:" + userID.String()) {
		sendError(w, http.StatusTooManyRequests, "Код уже отправлен. Попробуйте позже.")
		return
	}
	code := generateOTPCode()
	s.storeCode("2fa_setup:"+userID.String(), code)

	if err := s.sendEmail(user.Email, code, "Sea Battle – код включения 2FA"); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка отправки кода на email")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Код отправлен на email"})
}

// ---------- 2FA проверка кода включения ----------

func (s *Server) Verify2FA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат")
		return
	}

	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	ok, blocked := s.verifyCode("2fa_setup:"+userID.String(), req.Code)
	if blocked {
		sendError(w, http.StatusTooManyRequests, "Слишком много попыток. Запросите новый код.")
		return
	}
	if !ok {
		sendError(w, http.StatusUnauthorized, "Неверный или истёкший код")
		return
	}

	if err := s.DB.EnableEmailOTP(r.Context(), userID); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка включения 2FA")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "2FA enabled"})
}
