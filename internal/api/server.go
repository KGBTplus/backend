package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var jwtKey = []byte("my_secret_key") // В реальности храни в ENV

type Server struct {
	DB *db.Queries
}

// Register — создание пользователя
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	// Дополнительная валидация (пример)
	if req.Username == "" || req.Password == "" {
		sendError(w, http.StatusBadRequest, "Логин и пароль обязательны")
		return
	}

	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)

	userRow, err := s.DB.CreateUser(r.Context(), db.CreateUserParams{
		Username:     req.Username,
		PasswordHash: string(hashedPassword),
	})

	if err != nil {
		// Здесь мы проверяем, не ошибка ли это уникальности (Postgres error code 23505)
		// Если используешь pgx или lib/pq, проверь тип ошибки
		sendError(w, http.StatusConflict, "Пользователь с таким именем уже существует")
		return
	}

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

	// Добавим проверку декодирования JSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	user, err := s.DB.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		// Теперь фронтенд получит: {"error": "Пользователь не найден"}
		sendError(w, http.StatusUnauthorized, "Пользователь не найден")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		// Теперь фронтенд получит: {"error": "Неверный пароль"}
		sendError(w, http.StatusUnauthorized, "Неверный пароль")
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user.ID,
		"exp": time.Now().Add(time.Hour * 24).Unix(),
	})
	tokenString, _ := token.SignedString(jwtKey)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": tokenString})
}
