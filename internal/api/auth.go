package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"time"
	"unicode/utf8"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// ---------- Регистрация ----------

func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if utf8.RuneCountInString(req.Username) < minUsernameLen || utf8.RuneCountInString(req.Username) > maxUsernameLen {
		sendError(w, http.StatusBadRequest, "Имя пользователя должно содержать от 4 до 16 символов")
		return
	}
	if !usernameRegex.MatchString(req.Username) {
		sendError(w, http.StatusBadRequest, "Имя пользователя может содержать только латинские буквы, цифры и _")
		return
	}
	if len(req.Password) < minPasswordLen || len(req.Password) > maxPasswordLen {
		sendError(w, http.StatusBadRequest, "Пароль должен содержать от 8 символов")
		return
	}
	if req.Email == "" {
		sendError(w, http.StatusBadRequest, "Email обязателен")
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		sendError(w, http.StatusBadRequest, "Некорректный email")
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка при создании пользователя")
		return
	}

	userRow, err := s.DB.CreateUser(r.Context(), db.CreateUserParams{
		Username:     req.Username,
		PasswordHash: string(hashedPassword),
		Email:        req.Email,
	})
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			var existingUser *db.GetUserByUsernameWithVerifiedRow
			var found bool

			switch pqErr.Constraint {
			case "users_username_key":
				u, e := s.DB.GetUserByUsernameWithVerified(r.Context(), req.Username)
				if e == nil {
					existingUser = &u
					found = true
				}
			case "users_email_key":
				u, e := s.DB.GetUserByEmailWithVerified(r.Context(), req.Email)
				if e == nil {
					existingUser = &db.GetUserByUsernameWithVerifiedRow{
						ID:              u.ID,
						Username:        u.Username,
						PasswordHash:    u.PasswordHash,
						Email:           u.Email,
						CreatedAt:       u.CreatedAt,
						EmailOtpEnabled: u.EmailOtpEnabled,
						EmailVerified:   u.EmailVerified,
					}
					found = true
				}
			default:
				sendError(w, http.StatusConflict, "Пользователь с таким именем или email уже существует")
				return
			}

			if !found || existingUser.EmailVerified {
				if pqErr.Constraint == "users_username_key" {
					sendError(w, http.StatusConflict, "Пользователь с таким именем уже существует")
				} else {
					sendError(w, http.StatusConflict, "Пользователь с таким email уже существует")
				}
				return
			}

			if existingUser.Email != req.Email {
				s.DB.UpdateUserEmail(r.Context(), db.UpdateUserEmailParams{
					ID:    existingUser.ID,
					Email: req.Email,
				})
			}
			if !s.checkCodeSendRateLimit("verify:" + existingUser.ID.String()) {
				sendError(w, http.StatusTooManyRequests, "Код уже отправлен. Попробуйте позже.")
				return
			}
			code := generateOTPCode()
			s.storeCode("verify:"+existingUser.ID.String(), code)
			log.Printf("[EMAIL] Code sent to %s (%s)", existingUser.Username, maskEmail(req.Email))
			if err := s.sendEmail(req.Email, code, "Sea Battle – подтверждение email"); err != nil {
				log.Printf("[EMAIL] sendEmail failed for %s: %v", req.Email, err)
			}
			tempToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
				"sub":  existingUser.ID.String(),
				"exp":  time.Now().Add(otpExpiry).Unix(),
				"type": "temp",
			})
			tokenString, _ := tempToken.SignedString(s.JWTKey)
			s.setTempCookie(w, tokenString)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"message": "Код отправлен на email",
			})
			return
		}
		sendError(w, http.StatusInternalServerError, "Ошибка при создании пользователя")
		return
	}

	if err := s.DB.CreateProfile(r.Context(), userRow.ID); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка при создании профиля")
		return
	}

	if !s.checkCodeSendRateLimit("verify:" + userRow.ID.String()) {
		sendError(w, http.StatusTooManyRequests, "Код уже отправлен. Попробуйте позже.")
		return
	}
	code := generateOTPCode()
	s.storeCode("verify:"+userRow.ID.String(), code)
	log.Printf("[EMAIL] Code sent to %s (%s)", userRow.Username, maskEmail(req.Email))
	if err := s.sendEmail(req.Email, code, "Sea Battle – подтверждение email"); err != nil {
		log.Printf("[EMAIL] sendEmail failed for %s: %v", req.Email, err)
	}

	tempToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  userRow.ID.String(),
		"exp":  time.Now().Add(otpExpiry).Unix(),
		"type": "temp",
	})
	tokenString, _ := tempToken.SignedString(s.JWTKey)
	s.setTempCookie(w, tokenString)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Код отправлен на email",
	})
}

// ---------- Подтверждение email ----------

func (s *Server) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TempToken string `json:"temp_token"`
		Code      string `json:"code"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат")
		return
	}

	tempToken := req.TempToken
	if tempToken == "" {
		if c, err := r.Cookie("temp_token"); err == nil {
			tempToken = c.Value
		}
	}

	userID, err := s.parseTempToken(tempToken)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Неверный или истёкший temp_token")
		return
	}

	ok, blocked := s.verifyCode("verify:"+userID.String(), req.Code)
	if blocked {
		sendError(w, http.StatusTooManyRequests, "Слишком много попыток. Запросите новый код.")
		return
	}
	if !ok {
		sendError(w, http.StatusUnauthorized, "Неверный или истёкший код")
		return
	}

	if err := s.DB.VerifyEmail(r.Context(), userID); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка подтверждения email")
		return
	}

	userWithVersion, err := s.DB.GetUserWithTokenVersion(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения данных пользователя")
		return
	}

	if _, err := s.issueTokens(w, userID, userWithVersion.TokenVersion); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка создания токена")
		return
	}
	s.clearTempCookie(w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":  userID,
		"username": userWithVersion.Username,
		"message":  "Email подтверждён",
	})
}

// ---------- Логин (с поддержкой 2FA) ----------

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	user, err := s.DB.GetUserByUsernameWithVerified(r.Context(), req.Username)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Неверное имя пользователя или пароль")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		sendError(w, http.StatusUnauthorized, "Неверное имя пользователя или пароль")
		return
	}

	if !user.EmailVerified {
		code := generateOTPCode()
		s.storeCode("verify:"+user.ID.String(), code)
		log.Printf("[EMAIL] Code sent to %s (%s)", user.Username, maskEmail(user.Email))
		if err := s.sendEmail(user.Email, code, "Sea Battle – подтверждение email"); err != nil {
			log.Printf("[EMAIL] sendEmail failed for %s: %v", user.Email, err)
		}

		tempToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":  user.ID.String(),
			"exp":  time.Now().Add(otpExpiry).Unix(),
			"type": "temp",
		})
		tokenString, _ := tempToken.SignedString(s.JWTKey)
		s.setTempCookie(w, tokenString)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Код отправлен на email",
		})
		return
	}

	if user.EmailOtpEnabled {
		if s.checkCodeSendRateLimit("login:" + user.ID.String()) {
			code := generateOTPCode()
			s.storeCode("login:"+user.ID.String(), code)

			if err := s.sendEmail(user.Email, code, "Sea Battle – код 2FA"); err != nil {
				sendError(w, http.StatusInternalServerError, "Ошибка отправки кода на email")
				return
			}
		}

		tempToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":  user.ID.String(),
			"exp":  time.Now().Add(otpExpiry).Unix(),
			"type": "temp",
		})
		tokenString, err := tempToken.SignedString(s.JWTKey)
		if err != nil {
			sendError(w, http.StatusInternalServerError, "Ошибка создания временного токена")
			return
		}
		s.setTempCookie(w, tokenString)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Код отправлен на email",
		})
		return
	}

	userWithVersion, err := s.DB.GetUserWithTokenVersion(r.Context(), user.ID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения данных пользователя")
		return
	}

	if _, err := s.issueTokens(w, user.ID, userWithVersion.TokenVersion); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка создания токена")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":  user.ID,
		"username": userWithVersion.Username,
	})
}

// ---------- 2FA: подтверждение кода после логина ----------

func (s *Server) Authenticate2FA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TempToken string `json:"temp_token"`
		Code      string `json:"code"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат")
		return
	}

	tempToken := req.TempToken
	if tempToken == "" {
		if c, err := r.Cookie("temp_token"); err == nil {
			tempToken = c.Value
		}
	}

	userID, err := s.parseTempToken(tempToken)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Неверный или истёкший temp_token")
		return
	}

	uid := userID.String()
	ok, blocked := s.verifyCode("login:"+uid, req.Code)
	isVerify := false
	if !ok && !blocked {
		ok, blocked = s.verifyCode("verify:"+uid, req.Code)
		isVerify = true
	}
	if blocked {
		sendError(w, http.StatusTooManyRequests, "Слишком много попыток. Запросите новый код.")
		return
	}
	if !ok {
		sendError(w, http.StatusUnauthorized, "Неверный или истёкший код")
		return
	}

	if isVerify {
		s.DB.VerifyEmail(r.Context(), userID)
	}

	userWithVersion, err := s.DB.GetUserWithTokenVersion(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения данных пользователя")
		return
	}

	if _, err := s.issueTokens(w, userID, userWithVersion.TokenVersion); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка создания токена")
		return
	}
	s.clearTempCookie(w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":  userID,
		"username": userWithVersion.Username,
	})
}

// ---------- Универсальная верификация OTP (фронтенд шлёт {username, code}) ----------

func (s *Server) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	user, err := s.DB.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Пользователь не найден")
		return
	}

	ok, blocked := s.verifyCode("login:"+user.ID.String(), req.Code)
	if blocked {
		sendError(w, http.StatusTooManyRequests, "Слишком много попыток. Запросите новый код.")
		return
	}
	if ok {
		userWithVersion, err := s.DB.GetUserWithTokenVersion(r.Context(), user.ID)
		if err != nil {
			sendError(w, http.StatusInternalServerError, "Ошибка получения данных пользователя")
			return
		}
		if _, err := s.issueTokens(w, user.ID, userWithVersion.TokenVersion); err != nil {
			sendError(w, http.StatusInternalServerError, "Ошибка создания токена")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user_id":  user.ID,
			"username": userWithVersion.Username,
		})
		return
	}

	ok, blocked = s.verifyCode("verify:"+user.ID.String(), req.Code)
	if blocked {
		sendError(w, http.StatusTooManyRequests, "Слишком много попыток. Запросите новый код.")
		return
	}
	if ok {
		if err := s.DB.VerifyEmail(r.Context(), user.ID); err != nil {
			sendError(w, http.StatusInternalServerError, "Ошибка подтверждения email")
			return
		}

		userWithVersion, err := s.DB.GetUserWithTokenVersion(r.Context(), user.ID)
		if err != nil {
			sendError(w, http.StatusInternalServerError, "Ошибка получения данных пользователя")
			return
		}
		if _, err := s.issueTokens(w, user.ID, userWithVersion.TokenVersion); err != nil {
			sendError(w, http.StatusInternalServerError, "Ошибка создания токена")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user_id":  user.ID,
			"username": userWithVersion.Username,
		})
		return
	}

	sendError(w, http.StatusUnauthorized, "Неверный или истёкший код")
}

// ---------- Refresh token ----------

func (s *Server) RefreshToken(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("refresh_token")
	if err != nil || c.Value == "" {
		sendError(w, http.StatusUnauthorized, "Refresh token отсутствует")
		return
	}

	token, err := jwt.Parse(c.Value, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.JWTKey, nil
	})
	if err != nil || !token.Valid {
		s.clearAuthCookie(w)
		s.clearRefreshCookie(w)
		sendError(w, http.StatusUnauthorized, "Невалидный refresh token")
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		s.clearAuthCookie(w)
		s.clearRefreshCookie(w)
		sendError(w, http.StatusUnauthorized, "Невалидный refresh token")
		return
	}

	tokenType, _ := claims["type"].(string)
	if tokenType != "refresh" {
		s.clearAuthCookie(w)
		s.clearRefreshCookie(w)
		sendError(w, http.StatusUnauthorized, "Не refresh token")
		return
	}

	userIDStr, ok := claims["sub"].(string)
	if !ok {
		s.clearAuthCookie(w)
		s.clearRefreshCookie(w)
		sendError(w, http.StatusUnauthorized, "Невалидный refresh token")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		s.clearAuthCookie(w)
		s.clearRefreshCookie(w)
		sendError(w, http.StatusUnauthorized, "Невалидный refresh token")
		return
	}

	user, err := s.DB.GetUserWithTokenVersion(r.Context(), userID)
	if err != nil {
		s.clearAuthCookie(w)
		s.clearRefreshCookie(w)
		sendError(w, http.StatusUnauthorized, "Пользователь не найден")
		return
	}

	if tv, hasVersion := claims["token_version"]; hasVersion {
		expectedVersion, ok := tv.(float64)
		if ok && int32(expectedVersion) != user.TokenVersion {
			s.clearAuthCookie(w)
			s.clearRefreshCookie(w)
			sendError(w, http.StatusUnauthorized, "Токен отозван")
			return
		}
	}

	if _, err := s.issueTokens(w, userID, user.TokenVersion); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка создания токена")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":  userID,
		"username": user.Username,
	})
}

// ---------- Logout (инвалидация токена) ----------

func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err == nil {
		s.DB.IncrementTokenVersion(r.Context(), userID)
	}
	s.clearAuthCookie(w)
	s.clearRefreshCookie(w)
	s.clearTempCookie(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "logged out"})
}

// ---------- Проверка аутентификации ----------

func (s *Server) AuthMe(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}
	user, err := s.DB.GetUserByID(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения пользователя")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":  user.ID,
		"username": user.Username,
	})
}

func (s *Server) WsToken(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	user, err := s.DB.GetUserWithTokenVersion(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения данных пользователя")
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":           userID.String(),
		"exp":           time.Now().Add(otpExpiry).Unix(),
		"type":          "ws",
		"token_version": user.TokenVersion,
	})
	tokenString, _ := token.SignedString(s.JWTKey)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": tokenString})
}
