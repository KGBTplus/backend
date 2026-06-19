package api

import (
	"encoding/json"
	"net/http"

	"github.com/KGBTplus/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if len(req.NewPassword) < minPasswordLen || len(req.NewPassword) > maxPasswordLen {
		sendError(w, http.StatusBadRequest, "Пароль должен содержать от 8 символов")
		return
	}

	user, err := s.DB.GetUserByID(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения пользователя")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)); err != nil {
		sendError(w, http.StatusUnauthorized, "Неверный текущий пароль")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptCost)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка при смене пароля")
		return
	}

	if err := s.DB.UpdatePassword(r.Context(), db.UpdatePasswordParams{
		ID:           userID,
		PasswordHash: string(hash),
	}); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка при смене пароля")
		return
	}
	s.DB.IncrementTokenVersion(r.Context(), userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "password changed"})
}

func (s *Server) SendForgotPasswordCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if req.Username == "" {
		sendError(w, http.StatusBadRequest, "Укажите username")
		return
	}

	user, err := s.DB.GetUserByUsernameWithVerified(r.Context(), req.Username)
	if err != nil || !user.EmailVerified {
		// Всегда возвращаем одинаковый ответ — не раскрываем, существует ли пользователь
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"email":   "",
			"message": "Если пользователь с таким именем существует и email подтверждён, код отправлен на почту",
		})
		return
	}

	if !s.checkCodeSendRateLimit("forgot_password:" + user.ID.String()) {
		sendError(w, http.StatusTooManyRequests, "Код уже отправлен. Попробуйте позже.")
		return
	}

	code := generateOTPCode()
	s.storeCode("forgot_password:"+user.ID.String(), code)

	if err := s.sendEmail(user.Email, code, "Sea Battle – сброс пароля"); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка отправки кода на email")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"email":   "",
		"message": "Если пользователь с таким именем существует и email подтверждён, код отправлен на почту",
	})
}

func (s *Server) ResetForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if len(req.NewPassword) < minPasswordLen || len(req.NewPassword) > maxPasswordLen {
		sendError(w, http.StatusBadRequest, "Пароль должен содержать от 8 символов")
		return
	}

	user, err := s.DB.GetUserByUsernameWithVerified(r.Context(), req.Username)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Пользователь не найден")
		return
	}

	ok, blocked := s.verifyCode("forgot_password:"+user.ID.String(), req.Code)
	if blocked {
		sendError(w, http.StatusTooManyRequests, "Слишком много попыток. Запросите новый код.")
		return
	}
	if !ok {
		sendError(w, http.StatusUnauthorized, "Неверный или истёкший код")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptCost)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка при смене пароля")
		return
	}

	if err := s.DB.UpdatePassword(r.Context(), db.UpdatePasswordParams{
		ID:           user.ID,
		PasswordHash: string(hash),
	}); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка при смене пароля")
		return
	}
	s.DB.IncrementTokenVersion(r.Context(), user.ID)

	s.rateMu.Lock()
	delete(s.codeRateLimits, "forgot_password:"+user.ID.String())
	s.rateMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "password reset"})
}
