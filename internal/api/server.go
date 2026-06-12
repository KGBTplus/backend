package api

import (
	"encoding/json"
	"errors"
	"fmt"
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
	DB     *db.Queries
	SMTP   SMTPConfig
	JWTKey []byte
	Games  *GameStore
	mu     sync.Mutex
	codes  map[string]otpEntry
}

func NewServer(db *db.Queries, smtp SMTPConfig, jwtSecret string) *Server {
	key := []byte(jwtSecret)
	if len(key) == 0 {
		key = []byte("my_secret_key")
	}
	return &Server{
		DB:     db,
		SMTP:   smtp,
		JWTKey: key,
		Games:  NewGameStore(),
		codes:  make(map[string]otpEntry),
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

	// TODO: DB call to disable 2FA
	_ = userID

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

	// TODO: DB call UpdateUsername
	_ = userID

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

	// TODO: verify old password and update in DB
	_ = userID

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
		"game_id":     game.ID,
		"player1_id":  game.Player1ID,
		"player2_id":  game.Player2ID,
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
	_, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	// TODO: implement DB query
	_ = params

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"top":     []interface{}{},
		"my_rank": nil,
	})
}

// ---------- Лобби (in-memory) ----------

type lobby struct {
	ID          uuid.UUID `json:"id"`
	CreatorID   uuid.UUID `json:"creator_id"`
	Status      string    `json:"status"`
	InviteCode  string    `json:"invite_code"`
	Players     []uuid.UUID `json:"players"`
	MaxPlayers  int       `json:"max_players"`
}

type lobbyStore struct {
	mu     sync.RWMutex
	items  map[uuid.UUID]*lobby
}

var globalLobbyStore = &lobbyStore{items: make(map[uuid.UUID]*lobby)}

func genInviteCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	code := make([]byte, 6)
	for i := range code {
		code[i] = chars[rand.Intn(len(chars))]
	}
	return string(code)
}

func (s *Server) ListLobbies(w http.ResponseWriter, r *http.Request, params ListLobbiesParams) {
	_, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	globalLobbyStore.mu.RLock()
	var result []*lobby
	for _, l := range globalLobbyStore.items {
		if params.Status != nil && l.Status != string(*params.Status) {
			continue
		}
		result = append(result, l)
	}
	globalLobbyStore.mu.RUnlock()
	if result == nil {
		result = []*lobby{}
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

	l := &lobby{
		ID:         uuid.New(),
		CreatorID:  userID,
		Status:     "waiting",
		InviteCode: genInviteCode(),
		Players:    []uuid.UUID{userID},
		MaxPlayers: 2,
	}
	globalLobbyStore.mu.Lock()
	globalLobbyStore.items[l.ID] = l
	globalLobbyStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(l)
}

func (s *Server) GetLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	globalLobbyStore.mu.RLock()
	l, ok := globalLobbyStore.items[uuid.UUID(lobbyID)]
	globalLobbyStore.mu.RUnlock()

	if !ok {
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}

	result := map[string]interface{}{
		"id":         l.ID,
		"creator_id": l.CreatorID,
		"status":     l.Status,
		"players":    l.Players,
		"max_players": l.MaxPlayers,
	}
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

	globalLobbyStore.mu.Lock()
	l, ok := globalLobbyStore.items[uuid.UUID(lobbyID)]
	if !ok {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}
	if l.Status != "waiting" {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusConflict, "Лобби уже заполнено")
		return
	}
	for _, p := range l.Players {
		if p == userID {
			globalLobbyStore.mu.Unlock()
			sendError(w, http.StatusConflict, "Вы уже в лобби")
			return
		}
	}
	if len(l.Players) >= l.MaxPlayers {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusConflict, "Лобби заполнено")
		return
	}
	l.Players = append(l.Players, userID)
	if len(l.Players) >= l.MaxPlayers {
		l.Status = "full"
	}
	globalLobbyStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(l)
}

func (s *Server) LeaveLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	globalLobbyStore.mu.Lock()
	l, ok := globalLobbyStore.items[uuid.UUID(lobbyID)]
	if !ok {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}
	var newPlayers []uuid.UUID
	found := false
	for _, p := range l.Players {
		if p == userID {
			found = true
		} else {
			newPlayers = append(newPlayers, p)
		}
	}
	if !found {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusBadRequest, "Вы не в этом лобби")
		return
	}
	l.Players = newPlayers
	if len(newPlayers) == 0 {
		delete(globalLobbyStore.items, l.ID)
		globalLobbyStore.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "lobby deleted"})
		return
	}
	l.Status = "waiting"
	globalLobbyStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(l)
}

func (s *Server) DeleteLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	globalLobbyStore.mu.Lock()
	l, ok := globalLobbyStore.items[uuid.UUID(lobbyID)]
	if !ok {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}
	if l.CreatorID != userID {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusForbidden, "Только создатель может удалить лобби")
		return
	}
	delete(globalLobbyStore.items, l.ID)
	globalLobbyStore.mu.Unlock()

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

	globalLobbyStore.mu.Lock()
	var found *lobby
	for _, l := range globalLobbyStore.items {
		if l.InviteCode == req.Code {
			found = l
			break
		}
	}
	if found == nil {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusNotFound, "Лобби с таким кодом не найдено")
		return
	}
	if found.Status != "waiting" {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusConflict, "Лобби уже заполнено")
		return
	}
	for _, p := range found.Players {
		if p == userID {
			globalLobbyStore.mu.Unlock()
			sendError(w, http.StatusConflict, "Вы уже в лобби")
			return
		}
	}
	if len(found.Players) >= found.MaxPlayers {
		globalLobbyStore.mu.Unlock()
		sendError(w, http.StatusConflict, "Лобби заполнено")
		return
	}
	found.Players = append(found.Players, userID)
	if len(found.Players) >= found.MaxPlayers {
		found.Status = "full"
	}
	globalLobbyStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(found)
}

// ---------- Матчмейкинг (in-memory) ----------

type matchmakingEntry struct {
	PlayerID  uuid.UUID
	JoinedAt  time.Time
}

type matchmakingStore struct {
	mu    sync.Mutex
	queue []matchmakingEntry
}

var globalMMStore = &matchmakingStore{}

func (s *Server) JoinMatchmaking(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	globalMMStore.mu.Lock()
	for _, e := range globalMMStore.queue {
		if e.PlayerID == userID {
			globalMMStore.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "already_in_queue"})
			return
		}
	}
	globalMMStore.queue = append(globalMMStore.queue, matchmakingEntry{
		PlayerID: userID,
		JoinedAt: time.Now(),
	})
	globalMMStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "searching"})
}

func (s *Server) GetMatchmakingStatus(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	globalMMStore.mu.Lock()
	inQueue := false
	for _, e := range globalMMStore.queue {
		if e.PlayerID == userID {
			inQueue = true
			break
		}
	}
	globalMMStore.mu.Unlock()

	if inQueue {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "searching"})
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "not_found"})
	}
}

func (s *Server) LeaveMatchmaking(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	globalMMStore.mu.Lock()
	var newQueue []matchmakingEntry
	for _, e := range globalMMStore.queue {
		if e.PlayerID != userID {
			newQueue = append(newQueue, e)
		}
	}
	globalMMStore.queue = newQueue
	globalMMStore.mu.Unlock()

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
