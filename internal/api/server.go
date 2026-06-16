package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"golang.org/x/crypto/bcrypt"
)

const (
	minUsernameLen = 4
	maxUsernameLen = 16
	minPasswordLen = 8
	maxPasswordLen = 20
	otpExpiry      = 5 * time.Minute
)

type SMTPConfig struct {
	Host     string
	Username string
	Password string
	From     string
}

type otpEntry struct {
	Code      string
	ExpiresAt time.Time
}

type Server struct {
	Unimplemented
	DB          *db.Queries
	SMTP        SMTPConfig
	JWTKey      []byte
	Games       *GameStore
	ArcadeGames *ArcadeStore
	mu          sync.Mutex
	codes       map[string]otpEntry
}

func NewServer(db *db.Queries, smtp SMTPConfig, jwtSecret string) *Server {
	key := []byte(jwtSecret)
	if len(key) == 0 {
		key = []byte("my_secret_key")
	}
	return &Server{
		DB:          db,
		SMTP:        smtp,
		JWTKey:      key,
		Games:       NewGameStore(),
		ArcadeGames: NewArcadeStore(),
		codes:       make(map[string]otpEntry),
	}
}

// ---------- Вспомогательные ----------

func sendError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Server) getUserIDFromToken(r *http.Request) (uuid.UUID, error) {
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
		return s.JWTKey, nil
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

func (s *Server) parseTempToken(tokenStr string) (uuid.UUID, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return s.JWTKey, nil
	})
	if err != nil || !token.Valid {
		return uuid.Nil, errors.New("invalid temp token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return uuid.Nil, errors.New("invalid claims")
	}
	tokenType, _ := claims["type"].(string)
	if tokenType != "temp" {
		return uuid.Nil, errors.New("not a temp token")
	}
	userIDStr, ok := claims["sub"].(string)
	if !ok {
		return uuid.Nil, errors.New("user id not found")
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, errors.New("invalid user id format")
	}
	return userID, nil
}

func generateOTPCode() string {
	return fmt.Sprintf("%06d", rand.Intn(1000000))
}

func (s *Server) sendEmail(to, code string) error {
	if s.SMTP.Host == "" {
		fmt.Printf("[EMAIL DEBUG] To: %s, Code: %s\n", to, code)
		return nil
	}

	auth := smtp.PlainAuth("", s.SMTP.Username, s.SMTP.Password, s.SMTP.Host)
	msg := []byte("From: " + s.SMTP.From + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: Sea Battle – код 2FA\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		"Ваш код для входа: " + code + "\r\n" +
		"Код действителен 5 минут.\r\n")

	addr := s.SMTP.Host + ":587"
	return smtp.SendMail(addr, auth, s.SMTP.From, []string{to}, msg)
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

	if err := s.DB.CreateProfile(r.Context(), userRow.ID); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка при создании профиля")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(userRow)
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

	user, err := s.DB.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Пользователь не найден")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		sendError(w, http.StatusUnauthorized, "Неверный пароль")
		return
	}

	if user.EmailOtpEnabled {
		code := generateOTPCode()
		s.mu.Lock()
		s.codes["login:"+user.ID.String()] = otpEntry{
			Code:      code,
			ExpiresAt: time.Now().Add(otpExpiry),
		}
		s.mu.Unlock()

		if err := s.sendEmail(user.Email, code); err != nil {
			sendError(w, http.StatusInternalServerError, "Ошибка отправки кода на email")
			return
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

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"temp_token": tokenString,
			"message":    "Код отправлен на email",
		})
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user.ID.String(),
		"exp": time.Now().Add(time.Hour * 24).Unix(),
	})
	tokenString, err := token.SignedString(s.JWTKey)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка создания токена")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": tokenString})
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

	userID, err := s.parseTempToken(req.TempToken)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Неверный или истёкший temp_token")
		return
	}

	key := "login:" + userID.String()
	s.mu.Lock()
	entry, ok := s.codes[key]
	if ok {
		delete(s.codes, key)
	}
	s.mu.Unlock()

	if !ok || time.Now().After(entry.ExpiresAt) {
		sendError(w, http.StatusUnauthorized, "Код истёк или не найден")
		return
	}

	if entry.Code != req.Code {
		sendError(w, http.StatusUnauthorized, "Неверный код")
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID.String(),
		"exp": time.Now().Add(time.Hour * 24).Unix(),
	})
	tokenString, err := token.SignedString(s.JWTKey)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка создания токена")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": tokenString})
}

// ---------- 2FA настройка (отправка кода на email) ----------

func (s *Server) Setup2FA(w http.ResponseWriter, r *http.Request) {
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
	if user.EmailOtpEnabled {
		sendError(w, http.StatusConflict, "2FA уже включена")
		return
	}

	code := generateOTPCode()
	s.mu.Lock()
	s.codes["setup:"+user.ID.String()] = otpEntry{
		Code:      code,
		ExpiresAt: time.Now().Add(otpExpiry),
	}
	s.mu.Unlock()

	if err := s.sendEmail(user.Email, code); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка отправки кода на email")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Код отправлен на email"})
}

// ---------- 2FA подтверждение и активация ----------

func (s *Server) Verify2FA(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
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
	if user.EmailOtpEnabled {
		sendError(w, http.StatusConflict, "2FA уже активирована")
		return
	}

	key := "setup:" + userID.String()
	s.mu.Lock()
	entry, ok := s.codes[key]
	if ok {
		delete(s.codes, key)
	}
	s.mu.Unlock()

	if !ok || time.Now().After(entry.ExpiresAt) {
		sendError(w, http.StatusBadRequest, "Код истёк или не найден. Сначала вызовите /auth/2fa/setup")
		return
	}

	if entry.Code != req.Code {
		sendError(w, http.StatusUnauthorized, "Неверный код")
		return
	}

	err = s.DB.EnableEmailOTP(r.Context(), user.ID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка активации 2FA")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "2FA activated"})
}

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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "2FA disabled"})
}

// ---------- Профиль ----------

func (s *Server) GetProfile(w http.ResponseWriter, r *http.Request) {
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

	profile, err := s.DB.GetProfile(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения профиля")
		return
	}

	var winPct, hitPct float64
	if profile.TotalGames > 0 {
		winPct = float64(profile.Wins) / float64(profile.TotalGames) * 100
	}
	if profile.TotalShots > 0 {
		hitPct = float64(profile.Hits) / float64(profile.TotalShots) * 100
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":             user.ID,
		"username":       user.Username,
		"total_games":    profile.TotalGames,
		"wins":           profile.Wins,
		"losses":         profile.Losses,
		"win_percentage": winPct,
		"ships_sunk":     profile.ShipsSunk,
		"total_shots":    profile.TotalShots,
		"hits":           profile.Hits,
		"hit_percentage": hitPct,
	})
}

func (s *Server) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if len(req.Username) < minUsernameLen || len(req.Username) > maxUsernameLen {
		sendError(w, http.StatusBadRequest, "Имя пользователя должно содержать от 4 до 16 символов")
		return
	}

	if err := s.DB.UpdateUsername(r.Context(), db.UpdateUsernameParams{
		ID:       userID,
		Username: req.Username,
	}); err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			sendError(w, http.StatusConflict, "Никнейм уже занят")
			return
		}
		sendError(w, http.StatusInternalServerError, "Ошибка обновления профиля")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"username": req.Username})
}

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
		sendError(w, http.StatusBadRequest, "Пароль должен содержать от 8 до 20 символов")
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

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "password changed"})
}

// ---------- Игры ----------

func (s *Server) CreateGame(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}
	game := s.Games.Create(userID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(game)
}

func (s *Server) ListGames(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}
	games := s.Games.ListAvailable(userID)
	if games == nil {
		games = []*GameRoom{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(games)
}

func (s *Server) GetActiveGames(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var active []*GameRoom
	for _, g := range s.Games.All() {
		if (g.Player1ID == userID || (g.Player2ID != nil && *g.Player2ID == userID)) &&
			(g.Status == "placing_ships" || g.Status == "playing") {
			active = append(active, g)
		}
	}
	if active == nil {
		active = []*GameRoom{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(active)
}

func (s *Server) GetGameHistory(w http.ResponseWriter, r *http.Request, params GetGameHistoryParams) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var finished []*GameRoom
	for _, g := range s.Games.All() {
		if (g.Player1ID == userID || (g.Player2ID != nil && *g.Player2ID == userID)) &&
			g.Status == "finished" {
			finished = append(finished, g)
		}
	}
	if finished == nil {
		finished = []*GameRoom{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(finished)
}

func (s *Server) GetGameState(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	_, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	game, ok := s.Games.Get(uuid.UUID(gameID))
	if !ok {
		sendError(w, http.StatusNotFound, "Игра не найдена")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(game)
}

func (s *Server) StartBotGame(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	game := s.Games.CreateBotGame(userID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(game)
}

func (s *Server) ForfeitGame(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	game, ok := s.Games.Get(uuid.UUID(gameID))
	if !ok {
		sendError(w, http.StatusNotFound, "Игра не найдена")
		return
	}
	if game.Status != "playing" {
		sendError(w, http.StatusBadRequest, "Игра не в статусе playing")
		return
	}
	if game.Player1ID != userID && (game.Player2ID == nil || *game.Player2ID != userID) {
		sendError(w, http.StatusForbidden, "Вы не участник этой игры")
		return
	}

	var winnerID uuid.UUID
	if userID == game.Player1ID {
		winnerID = *game.Player2ID
	} else {
		winnerID = game.Player1ID
	}
	game.Status = "finished"
	game.WinnerID = &winnerID
	game.CurrentTurn = nil

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(game)
}

func (s *Server) MakeMove(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if req.X < 0 || req.X > 9 || req.Y < 0 || req.Y > 9 {
		sendError(w, http.StatusBadRequest, "Координаты вне поля (0-9)")
		return
	}

	game, errMsg := s.Games.MakeMove(uuid.UUID(gameID), userID, req.X, req.Y)
	if errMsg != "" {
		sendError(w, http.StatusBadRequest, errMsg)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(game)
}

func (s *Server) GetGameResult(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	game, ok := s.Games.Get(uuid.UUID(gameID))
	if !ok {
		sendError(w, http.StatusNotFound, "Игра не найдена")
		return
	}
	if game.Status != "finished" {
		sendError(w, http.StatusBadRequest, "Игра ещё не завершена")
		return
	}
	if game.Player1ID != userID && (game.Player2ID == nil || *game.Player2ID != userID) {
		sendError(w, http.StatusForbidden, "Вы не участник этой игры")
		return
	}

	var winnerID *uuid.UUID
	if game.WinnerID != nil {
		wid := *game.WinnerID
		winnerID = &wid
	}

	var player2ID *uuid.UUID
	if game.Player2ID != nil {
		pid := *game.Player2ID
		player2ID = &pid
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"game_id":     game.ID,
		"player1_id":  game.Player1ID,
		"player2_id":  player2ID,
		"winner_id":   winnerID,
		"win_reason":  "surrender",
		"player1_mmr": 100,
		"player2_mmr": 100,
		"mmr_change":  0,
	})
}

func (s *Server) GetGameReplay(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	game, ok := s.Games.Get(uuid.UUID(gameID))
	if !ok {
		sendError(w, http.StatusNotFound, "Игра не найдена")
		return
	}
	if game.Player1ID != userID && (game.Player2ID == nil || *game.Player2ID != userID) {
		sendError(w, http.StatusForbidden, "Вы не участник этой игры")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"game_id":    game.ID,
		"player1_id": game.Player1ID,
		"player2_id": game.Player2ID,
		"initial_board": map[string]interface{}{
			"player1_ships": s.Games.PlayerShips(game, game.Player1ID),
			"player2_ships": nil,
		},
		"moves":  game.Moves,
		"status": game.Status,
	})
}

func (s *Server) RequestRematch(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	_, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	_, ok := s.Games.Get(uuid.UUID(gameID))
	if !ok {
		sendError(w, http.StatusNotFound, "Игра не найдена")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "rematch_requested"})
}

func (s *Server) PlaceShips(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		Ships []struct {
			ShipType   int  `json:"ship_type"`
			StartX     int  `json:"start_x"`
			StartY     int  `json:"start_y"`
			Horizontal bool `json:"horizontal"`
		} `json:"ships"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if len(req.Ships) != 10 {
		sendError(w, http.StatusBadRequest, "Должно быть ровно 10 кораблей")
		return
	}

	ships := make([]Ship, len(req.Ships))
	board := make([][]bool, 10)
	for i := range board {
		board[i] = make([]bool, 10)
	}

	for i, rs := range req.Ships {
		if rs.ShipType < 1 || rs.ShipType > 4 {
			sendError(w, http.StatusBadRequest, "Некорректный тип корабля")
			return
		}
		if rs.StartX < 0 || rs.StartX > 9 || rs.StartY < 0 || rs.StartY > 9 {
			sendError(w, http.StatusBadRequest, "Корабль за пределами поля")
			return
		}

		for j := 0; j < rs.ShipType; j++ {
			cx := rs.StartX
			cy := rs.StartY
			if rs.Horizontal {
				cx += j
			} else {
				cy += j
			}
			if cx < 0 || cx > 9 || cy < 0 || cy > 9 {
				sendError(w, http.StatusBadRequest, "Корабль за пределами поля")
				return
			}
			if board[cy][cx] {
				sendError(w, http.StatusBadRequest, "Корабли пересекаются")
				return
			}
			board[cy][cx] = true
		}

		ships[i] = Ship{
			ShipType:   rs.ShipType,
			StartX:     rs.StartX,
			StartY:     rs.StartY,
			Horizontal: rs.Horizontal,
		}
	}

	typeCount := map[int]int{4: 1, 3: 2, 2: 3, 1: 4}
	for _, s := range ships {
		typeCount[s.ShipType]--
	}
	for _, c := range typeCount {
		if c != 0 {
			sendError(w, http.StatusBadRequest, "Неверный набор кораблей: нужно 1×4, 2×3, 3×2, 4×1")
			return
		}
	}

	ok := s.Games.PlaceShips(uuid.UUID(gameID), userID, ships)
	if !ok {
		sendError(w, http.StatusBadRequest, "Не удалось расставить корабли")
		return
	}

	s.Games.CheckAndStart(uuid.UUID(gameID))
	game, _ := s.Games.Get(uuid.UUID(gameID))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(game)
}

func (s *Server) ConfirmShips(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	game, ok := s.Games.Get(uuid.UUID(gameID))
	if !ok {
		sendError(w, http.StatusNotFound, "Игра не найдена")
		return
	}
	if game.Player1ID != userID && (game.Player2ID == nil || *game.Player2ID != userID) {
		sendError(w, http.StatusForbidden, "Вы не участник этой игры")
		return
	}

	shipCount := 0
	for _, s := range game.Ships {
		if s.PlayerID == userID {
			shipCount++
		}
	}
	if shipCount < 10 {
		sendError(w, http.StatusBadRequest, "Расставьте все 10 кораблей")
		return
	}

	s.Games.CheckAndStart(uuid.UUID(gameID))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(game)
}

func (s *Server) PlaceShipsRandom(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	game, ok := s.Games.Get(uuid.UUID(gameID))
	if !ok {
		sendError(w, http.StatusNotFound, "Игра не найдена")
		return
	}
	if game.Player1ID != userID && (game.Player2ID == nil || *game.Player2ID != userID) {
		sendError(w, http.StatusForbidden, "Вы не участник этой игры")
		return
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	shipDefs := map[int]int{4: 1, 3: 2, 2: 3, 1: 4}
	var randomShips []Ship

	for size, count := range shipDefs {
		for k := 0; k < count; k++ {
			for attempts := 0; attempts < 100; attempts++ {
				horizontal := rng.Intn(2) == 0
				startX := rng.Intn(10)
				startY := rng.Intn(10)
				if horizontal && startX+size > 10 {
					continue
				}
				if !horizontal && startY+size > 10 {
					continue
				}

				cells := make([][2]int, size)
				for j := 0; j < size; j++ {
					x := startX
					y := startY
					if horizontal {
						x += j
					} else {
						y += j
					}
					cells[j] = [2]int{x, y}
				}

				conflict := false
				for _, existing := range randomShips {
					for _, c := range cells {
						for ex := existing.StartX - 1; ex <= existing.StartX+existing.ShipType; ex++ {
							for ey := existing.StartY - 1; ey <= existing.StartY+1; ey++ {
								if existing.Horizontal {
									if ex >= 0 && ex < 10 && ey >= 0 && ey < 10 && c[0] == ex && c[1] == ey {
										conflict = true
									}
								} else {
									if ex >= 0 && ex < 10 && ey >= 0 && ey < 10 && c[0] == ex && c[1] == ey {
										conflict = true
									}
								}
							}
						}
					}
				}
				if !conflict {
					randomShips = append(randomShips, Ship{
						ShipType:   size,
						StartX:     startX,
						StartY:     startY,
						Horizontal: horizontal,
					})
					break
				}
			}
		}
	}

	if len(randomShips) != 10 {
		randomShips = generateSimpleShips(rng)
	}

	ok = s.Games.PlaceShips(uuid.UUID(gameID), userID, randomShips)
	if !ok {
		sendError(w, http.StatusBadRequest, "Не удалось расставить корабли")
		return
	}

	s.Games.CheckAndStart(uuid.UUID(gameID))
	game, _ = s.Games.Get(uuid.UUID(gameID))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(game)
}

func (s *Server) ResetShips(w http.ResponseWriter, r *http.Request, gameID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	game, ok := s.Games.Get(uuid.UUID(gameID))
	if !ok {
		sendError(w, http.StatusNotFound, "Игра не найдена")
		return
	}
	if game.Player1ID != userID && (game.Player2ID == nil || *game.Player2ID != userID) {
		sendError(w, http.StatusForbidden, "Вы не участник этой игры")
		return
	}

	var kept []Ship
	for _, s := range game.Ships {
		if s.PlayerID != userID {
			kept = append(kept, s)
		}
	}
	game.Ships = kept

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(game)
}

// ---------- Лидерборд ----------

func (s *Server) GetLeaderboard(w http.ResponseWriter, r *http.Request, params GetLeaderboardParams) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	limit := int32(50)
	if params.Limit != nil && *params.Limit > 0 {
		limit = int32(*params.Limit)
	}

	rows, err := s.DB.GetLeaderboard(r.Context(), limit)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения таблицы лидеров")
		return
	}

	var top []map[string]interface{}
	for _, r := range rows {
		top = append(top, map[string]interface{}{
			"rank":        r.Rank,
			"player_id":   r.PlayerID,
			"username":    r.Username,
			"wins":        r.Wins,
			"losses":      r.Losses,
			"total_games": r.TotalGames,
			"win_rate":    r.WinRate,
			"hit_rate":    r.HitRate,
		})
	}

	var myRank interface{}
	myRankRow, err := s.DB.GetPlayerRank(r.Context(), userID)
	if err == nil {
		winRate := 0.0
		if myRankRow.TotalGames > 0 {
			winRate = float64(myRankRow.Wins) / float64(myRankRow.TotalGames) * 100
		}
		hitRate := 0.0
		if myRankRow.TotalShots > 0 {
			hitRate = float64(myRankRow.Hits) / float64(myRankRow.TotalShots) * 100
		}
		myRank = map[string]interface{}{
			"player_id":   myRankRow.ID,
			"username":    myRankRow.Username,
			"wins":        myRankRow.Wins,
			"losses":      myRankRow.Losses,
			"total_games": myRankRow.TotalGames,
			"win_rate":    winRate,
			"hit_rate":    hitRate,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"top":     top,
		"my_rank": myRank,
	})
}

// ---------- Лобби ----------

func genInviteCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	code := make([]byte, 6)
	for i := range code {
		code[i] = chars[rand.Intn(len(chars))]
	}
	return string(code)
}

func lobbyToMap(l db.Lobby, players []uuid.UUID) map[string]interface{} {
	m := map[string]interface{}{
		"id":          l.ID,
		"creator_id":  l.CreatorID,
		"status":      l.Status,
		"players":     players,
		"max_players": l.MaxPlayers,
	}
	return m
}

func (s *Server) ListLobbies(w http.ResponseWriter, r *http.Request, params ListLobbiesParams) {
	_, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	status := ""
	if params.Status != nil {
		status = string(*params.Status)
	}

	rows, err := s.DB.ListLobbies(r.Context(), db.ListLobbiesParams{
		Limit:  100,
		Offset: 0,
		Status: status,
	})
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения лобби")
		return
	}

	var result []map[string]interface{}
	for _, l := range rows {
		players, _ := s.DB.GetLobbyPlayers(r.Context(), l.ID)
		if players == nil {
			players = []uuid.UUID{}
		}
		result = append(result, lobbyToMap(l, players))
	}
	if result == nil {
		result = []map[string]interface{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) CreateLobby(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	l, err := s.DB.CreateLobby(r.Context(), db.CreateLobbyParams{
		ID:         uuid.New(),
		CreatorID:  userID,
		InviteCode: genInviteCode(),
		MaxPlayers: 2,
	})
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка создания лобби")
		return
	}

	if err := s.DB.AddLobbyPlayer(r.Context(), db.AddLobbyPlayerParams{
		LobbyID:  l.ID,
		PlayerID: userID,
	}); err != nil {
		s.DB.DeleteLobby(r.Context(), l.ID)
		sendError(w, http.StatusInternalServerError, "Ошибка создания лобби")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(lobbyToMap(l, []uuid.UUID{userID}))
}

func (s *Server) GetLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	l, err := s.DB.GetLobby(r.Context(), uuid.UUID(lobbyID))
	if err != nil {
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}

	players, err := s.DB.GetLobbyPlayers(r.Context(), l.ID)
	if err != nil || players == nil {
		players = []uuid.UUID{}
	}

	result := lobbyToMap(l, players)
	if l.CreatorID == userID {
		result["invite_code"] = l.InviteCode
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) JoinLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	l, err := s.DB.GetLobby(r.Context(), uuid.UUID(lobbyID))
	if err != nil {
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}
	if l.Status != "waiting" {
		sendError(w, http.StatusConflict, "Лобби уже заполнено")
		return
	}

	exists, err := s.DB.IsPlayerInLobby(r.Context(), db.IsPlayerInLobbyParams{
		LobbyID:  l.ID,
		PlayerID: userID,
	})
	if err == nil && exists {
		sendError(w, http.StatusConflict, "Вы уже в лобби")
		return
	}

	if err := s.DB.AddLobbyPlayer(r.Context(), db.AddLobbyPlayerParams{
		LobbyID:  l.ID,
		PlayerID: userID,
	}); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка присоединения к лобби")
		return
	}

	players, _ := s.DB.GetLobbyPlayers(r.Context(), l.ID)
	if len(players) >= int(l.MaxPlayers) {
		s.DB.UpdateLobbyStatus(r.Context(), db.UpdateLobbyStatusParams{
			ID:     l.ID,
			Status: "full",
		})
		l.Status = "full"
	}

	if players == nil {
		players = []uuid.UUID{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lobbyToMap(l, players))
}

func (s *Server) LeaveLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	if _, err := s.DB.GetLobby(r.Context(), uuid.UUID(lobbyID)); err != nil {
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}

	if err := s.DB.RemoveLobbyPlayer(r.Context(), db.RemoveLobbyPlayerParams{
		LobbyID:  uuid.UUID(lobbyID),
		PlayerID: userID,
	}); err != nil {
		sendError(w, http.StatusBadRequest, "Вы не в этом лобби")
		return
	}

	players, _ := s.DB.GetLobbyPlayers(r.Context(), uuid.UUID(lobbyID))
	if len(players) == 0 {
		s.DB.DeleteLobby(r.Context(), uuid.UUID(lobbyID))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "lobby deleted"})
		return
	}

	s.DB.UpdateLobbyStatus(r.Context(), db.UpdateLobbyStatusParams{
		ID:     uuid.UUID(lobbyID),
		Status: "waiting",
	})

	l, _ := s.DB.GetLobby(r.Context(), uuid.UUID(lobbyID))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lobbyToMap(l, players))
}

func (s *Server) DeleteLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	l, err := s.DB.GetLobby(r.Context(), uuid.UUID(lobbyID))
	if err != nil {
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}
	if l.CreatorID != userID {
		sendError(w, http.StatusForbidden, "Только создатель может удалить лобби")
		return
	}

	s.DB.DeleteLobby(r.Context(), l.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "lobby deleted"})
}

func (s *Server) JoinLobbyByCode(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	l, err := s.DB.FindLobbyByCode(r.Context(), req.Code)
	if err != nil {
		sendError(w, http.StatusNotFound, "Лобби с таким кодом не найдено")
		return
	}
	if l.Status != "waiting" {
		sendError(w, http.StatusConflict, "Лобби уже заполнено")
		return
	}

	if err := s.DB.AddLobbyPlayer(r.Context(), db.AddLobbyPlayerParams{
		LobbyID:  l.ID,
		PlayerID: userID,
	}); err != nil {
		sendError(w, http.StatusConflict, "Вы уже в лобби")
		return
	}

	players, _ := s.DB.GetLobbyPlayers(r.Context(), l.ID)
	if len(players) >= int(l.MaxPlayers) {
		s.DB.UpdateLobbyStatus(r.Context(), db.UpdateLobbyStatusParams{
			ID:     l.ID,
			Status: "full",
		})
		l.Status = "full"
	}
	if players == nil {
		players = []uuid.UUID{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lobbyToMap(l, players))
}

// ---------- Матчмейкинг ----------

func (s *Server) JoinMatchmaking(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	if err := s.DB.JoinMatchmaking(r.Context(), userID); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "searching"})
}

func (s *Server) GetMatchmakingStatus(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	inQueue, err := s.DB.GetMatchmakingStatus(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка")
		return
	}

	status := "not_found"
	if inQueue {
		status = "searching"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func (s *Server) LeaveMatchmaking(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	s.DB.LeaveMatchmaking(r.Context(), userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "left_queue"})
}

// ---------- Вспомогательное для генерации кораблей ----------

func generateSimpleShips(rng *rand.Rand) []Ship {
	var result []Ship
	shipDefs := map[int]int{4: 1, 3: 2, 2: 3, 1: 4}

	// Простой fallback — корабли "лесенкой"
	occupied := make([][]bool, 10)
	for i := range occupied {
		occupied[i] = make([]bool, 10)
	}

	for size, count := range shipDefs {
		for k := 0; k < count; k++ {
			placed := false
			for y := 0; y < 10 && !placed; y++ {
				for x := 0; x < 10 && !placed; x++ {
					horizontal := rng.Intn(2) == 0
					if horizontal && x+size > 10 {
						continue
					}
					if !horizontal && y+size > 10 {
						continue
					}
					ok := true
					for j := 0; j < size; j++ {
						cx, cy := x, y
						if horizontal {
							cx += j
						} else {
							cy += j
						}
						if occupied[cy][cx] {
							ok = false
							break
						}
						for dy := -1; dy <= 1 && ok; dy++ {
							for dx := -1; dx <= 1 && ok; dx++ {
								nx, ny := cx+dx, cy+dy
								if nx >= 0 && nx < 10 && ny >= 0 && ny < 10 && occupied[ny][nx] {
									if !(nx == cx && ny == cy) {
										ok = false
									}
								}
							}
						}
					}
					if ok {
						for j := 0; j < size; j++ {
							cx, cy := x, y
							if horizontal {
								cx += j
							} else {
								cy += j
							}
							occupied[cy][cx] = true
							for dy := -1; dy <= 1; dy++ {
								for dx := -1; dx <= 1; dx++ {
									nx, ny := cx+dx, cy+dy
									if nx >= 0 && nx < 10 && ny >= 0 && ny < 10 {
										occupied[ny][nx] = true
									}
								}
							}
						}
						result = append(result, Ship{
							ShipType:   size,
							StartX:     x,
							StartY:     y,
							Horizontal: horizontal,
						})
						placed = true
					}
				}
			}
		}
	}
	return result
}

// ---------- Аркадный режим (Морской бой / Торпедная атака) ----------

func (s *Server) StartArcade(w http.ResponseWriter, r *http.Request) {
	sess := s.ArcadeGames.Create()

	elapsed := time.Since(sess.StartedAt).Seconds()
	shipX := calcShipX(sess.ShipSpeed, sess.ShipPhase, elapsed)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"session_id":     sess.ID,
		"score":          sess.Score,
		"torpedoes_left": sess.TorpedoesLeft,
		"ship_x":         math.Round(shipX),
		"ship_speed":     sess.ShipSpeed,
		"trajectories":   trajectoryX[:],
		"travel_times":   travelTime[:],
		"viewport_width": 1000,
		"status":         sess.Status,
	})
}

func (s *Server) ShootTorpedo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID  string `json:"session_id"`
		Trajectory int    `json:"trajectory"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	sess, hit, errMsg := s.ArcadeGames.Shoot(req.SessionID, req.Trajectory)
	if errMsg != "" {
		sendError(w, http.StatusBadRequest, errMsg)
		return
	}

	elapsed := time.Since(sess.StartedAt).Seconds()
	shipX := calcShipX(sess.ShipSpeed, sess.ShipPhase, elapsed)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"hit":            hit,
		"score":          sess.Score,
		"torpedoes_left": sess.TorpedoesLeft,
		"ship_x":         math.Round(shipX),
		"status":         sess.Status,
	})
}
