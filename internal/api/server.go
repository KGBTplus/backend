package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"database/sql"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	minUsernameLen = 4
	maxUsernameLen = 16
	minPasswordLen = 8
	maxPasswordLen = 20
)

var jwtKey = []byte("my_secret_key") // TODO: вынести в ENV

type Server struct {
	DB *db.Queries
}

// ---------- Вспомогательные ----------
func sendError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// getUserIDFromToken извлекает userID из JWT и возвращает как uuid.UUID
func getUserIDFromToken(r *http.Request) (uuid.UUID, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return uuid.Nil, errors.New("missing Authorization header")
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return uuid.Nil, errors.New("invalid Authorization header format")
	}
	tokenStr := parts[1]

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return jwtKey, nil
	})
	if err != nil || !token.Valid {
		return uuid.Nil, errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return uuid.Nil, errors.New("invalid claims")
	}
	userIDStr, ok := claims["sub"].(string)
	if !ok {
		return uuid.Nil, errors.New("user id not found in token")
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, errors.New("invalid user id format")
	}
	return userID, nil
}

// generateTOTPSecret создаёт TOTP секрет и URL для QR-кода
func generateTOTPSecret(username string) (secret string, qrURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "SeaBattle",
		AccountName: username,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

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

	if len(req.Username) < minUsernameLen || len(req.Username) > maxUsernameLen {
		sendError(w, http.StatusBadRequest, "Имя пользователя должно содержать от 4 до 16 символов")
		return
	}
	if len(req.Password) < minPasswordLen || len(req.Password) > maxPasswordLen {
		sendError(w, http.StatusBadRequest, "Пароль должен содержать от 8 до 20 символов")
		return
	}
	if req.Email == "" {
		sendError(w, http.StatusBadRequest, "Email обязателен")
		return
	}
	if !strings.Contains(req.Email, "@") {
		sendError(w, http.StatusBadRequest, "Некорректный email")
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
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
			switch pqErr.Constraint {
			case "users_username_key":
				sendError(w, http.StatusConflict, "Пользователь с таким именем уже существует")
			case "users_email_key":
				sendError(w, http.StatusConflict, "Пользователь с таким email уже существует")
			default:
				sendError(w, http.StatusConflict, "Пользователь с таким именем или email уже существует")
			}
			return
		}
		sendError(w, http.StatusInternalServerError, "Ошибка при создании пользователя")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(userRow)
}

// ---------- Логин (без 2FA) ----------
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
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

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		sendError(w, http.StatusUnauthorized, "Неверный пароль")
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user.ID.String(),
		"exp": time.Now().Add(time.Hour * 24).Unix(),
	})
	tokenString, err := token.SignedString(jwtKey)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка создания токена")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": tokenString})
}

// ---------- 2FA настройка ----------
// Setup2FA – генерирует секрет и QR-код (требует JWT)
// Setup2FA – генерирует секрет и QR-код (требует JWT)
func (s *Server) Setup2FA(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	user, err := s.DB.GetUserByID(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения пользователя")
		return
	}
	if user.OtpEnabled.Valid && user.OtpEnabled.Bool {
		sendError(w, http.StatusConflict, "2FA уже включена")
		return
	}

	secret, qrURL, err := generateTOTPSecret(user.Username)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка генерации секрета")
		return
	}

	err = s.DB.UpdateOTPSecret(r.Context(), db.UpdateOTPSecretParams{
		ID:        user.ID,
		OtpSecret: sql.NullString{String: secret, Valid: true},
	})
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка сохранения секрета")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"secret": secret,
		"qr_url": qrURL,
	})
}

// Verify2FA – подтверждает код и активирует 2FA (требует JWT)
func (s *Server) Verify2FA(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат")
		return
	}

	user, err := s.DB.GetUserByID(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения пользователя")
		return
	}
	if user.OtpEnabled.Valid && user.OtpEnabled.Bool {
		sendError(w, http.StatusConflict, "2FA уже активирована")
		return
	}
	if !user.OtpSecret.Valid || user.OtpSecret.String == "" {
		sendError(w, http.StatusBadRequest, "2FA не настроена. Сначала вызовите /auth/2fa/setup")
		return
	}

	valid := totp.Validate(req.Code, user.OtpSecret.String)
	if !valid {
		sendError(w, http.StatusUnauthorized, "Неверный код")
		return
	}

	err = s.DB.EnableOTP(r.Context(), user.ID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка активации 2FA")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "2FA activated"})
}
