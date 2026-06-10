package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lib/pq"
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

// Register — создание пользователя
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

	// Валидация username
	if len(req.Username) < minUsernameLen || len(req.Username) > maxUsernameLen {
		sendError(w, http.StatusBadRequest, "Имя пользователя должно содержать от 4 до 16 символов")
		return
	}

	// Валидация пароля
	if len(req.Password) < minPasswordLen || len(req.Password) > maxPasswordLen {
		sendError(w, http.StatusBadRequest, "Пароль должен содержать от 8 до 20 символов")
		return
	}

	// Валидация email
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
		if errors.As(err, &pqErr) && pqErr.Code == "23505" { // unique violation
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

func sendError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// Login — проверка и выдача JWT
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
		"sub": user.ID,
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
