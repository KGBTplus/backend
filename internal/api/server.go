package api

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"math/rand"
	"net/http"
	"net/smtp"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	minUsernameLen     = 4
	maxUsernameLen     = 16
	minPasswordLen     = 8
	maxPasswordLen     = 128
	otpExpiry          = 5 * time.Minute
	codeSendCooldown   = 60 * time.Second
	maxCodeAttempts    = 5
	bcryptCost         = 12
	boardSize          = 10
	maxShips           = 10
	accessTokenExpiry  = 15 * time.Minute
	refreshTokenExpiry = 7 * 24 * time.Hour
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

type SMTPConfig struct {
	Host     string
	Username string
	Password string
	From     string
}

type otpEntry struct {
	Code      string
	ExpiresAt time.Time
	Attempts  int
}

type Server struct {
	Unimplemented
	DB             *db.Queries
	SMTP           SMTPConfig
	JWTKey         []byte
	Games          *GameStore
	Hub            *Hub
	Timers         *TimerManager
	SecureCookies  bool
	codesMu        sync.Mutex
	codes          map[string]otpEntry
	rateMu         sync.Mutex
	codeRateLimits map[string]time.Time
}

func NewServer(db *db.Queries, smtp SMTPConfig, jwtSecret string, opts ...ServerOption) *Server {
	key := []byte(jwtSecret)
	s := &Server{
		DB:             db,
		SMTP:           smtp,
		JWTKey:         key,
		Games:          NewGameStore(db),
		Hub:            NewHub(),
		Timers:         NewTimerManager(),
		codes:          make(map[string]otpEntry),
		codeRateLimits: make(map[string]time.Time),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type ServerOption func(*Server)

func WithSecureCookies(secure bool) ServerOption {
	return func(s *Server) {
		s.SecureCookies = secure
	}
}

// ---------- Вспомогательные ----------

func (s *Server) setAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(accessTokenExpiry.Seconds()),
	})
}

func (s *Server) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) setTempCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "temp_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(otpExpiry.Seconds()),
	})
}

func (s *Server) clearTempCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "temp_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.SecureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(refreshTokenExpiry.Seconds()),
	})
}

func (s *Server) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.SecureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func (s *Server) issueTokens(w http.ResponseWriter, userID uuid.UUID, tokenVersion int32) (string, error) {
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":           userID.String(),
		"exp":           time.Now().Add(accessTokenExpiry).Unix(),
		"token_version": tokenVersion,
		"type":          "access",
	})
	accessStr, err := accessToken.SignedString(s.JWTKey)
	if err != nil {
		return "", err
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":           userID.String(),
		"exp":           time.Now().Add(refreshTokenExpiry).Unix(),
		"token_version": tokenVersion,
		"type":          "refresh",
	})
	refreshStr, _ := refreshToken.SignedString(s.JWTKey)

	s.setAuthCookie(w, accessStr)
	s.setRefreshCookie(w, refreshStr)
	return accessStr, nil
}

func sendError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// ---------- WebSocket broadcast helpers ----------

func (s *Server) broadcastOpponentShipsPlaced(gameID uuid.UUID, userID uuid.UUID) {
	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}
	room.Broadcast(WSMessage{
		Type: "opponent_ships_placed",
	}, userID)
}

func (s *Server) broadcastOpponentReady(gameID uuid.UUID, userID uuid.UUID) {
	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}
	room.Broadcast(WSMessage{
		Type: "opponent_ready",
	}, userID)
}

func (s *Server) broadcastGameStarted(gameID uuid.UUID) {
	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}
	game, ok := s.Games.Get(gameID)
	if !ok {
		return
	}
	currentTurn := ""
	if game.CurrentTurn != nil {
		currentTurn = game.CurrentTurn.String()
	}
	msg := WSMessage{
		Type: "game_started",
		Data: mustJSON(GameStartedData{
			GameID:      gameID.String(),
			CurrentTurn: currentTurn,
		}),
	}
	room.mu.RLock()
	for _, c := range room.Clients {
		c.SendJSON(msg)
	}
	room.mu.RUnlock()
}

func (s *Server) broadcastOpponentMoved(gameID uuid.UUID, userID uuid.UUID, x, y int, game *GameRoom) {
	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}
	var sunk bool
	var sunkCells [][2]int
	if len(game.Moves) > 0 {
		last := game.Moves[len(game.Moves)-1]
		sunk = last.SunkShipID != nil
		if sunk {
			for _, ship := range game.Ships {
				if ship.ID == *last.SunkShipID {
					for _, cell := range ship.Cells {
						sunkCells = append(sunkCells, [2]int{cell.X, cell.Y})
					}
					break
				}
			}
		}
	}
	msg := WSMessage{
		Type: "opponent_moved",
		Data: mustJSON(OpponentMovedData{
			GameID:    gameID.String(),
			X:         x,
			Y:         y,
			Hit:       lastMoveHit(game),
			ShipSunk:  sunk,
			SunkCells: sunkCells,
		}),
	}
	room.Broadcast(msg, userID)
}

func (s *Server) broadcastYourTurn(gameID uuid.UUID, currentTurn uuid.UUID) {
	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}
	room.mu.RLock()
	defer room.mu.RUnlock()
	for _, c := range room.Clients {
		c.SendJSON(WSMessage{
			Type: "your_turn",
			Data: mustJSON(YourTurnData{
				GameID:       gameID.String(),
				MoveDeadline: time.Now().Add(time.Duration(TurnTimerDuration) * time.Second).Format(time.RFC3339),
				CurrentTurn:  currentTurn.String(),
			}),
		})
	}
}

func (s *Server) broadcastGameOver(gameID uuid.UUID, winnerID uuid.UUID, winReason string, result string) {
	s.Games.Lock()
	game, ok := s.Games.GetLocked(gameID)
	if !ok || game.isGameOverBroadcasted {
		s.Games.Unlock()
		return
	}
	game.isGameOverBroadcasted = true
	s.Games.Unlock()

	ctx := context.Background()
	s.DB.DeleteUserLobbies(ctx, game.Player1ID)
	if game.Player2ID != nil {
		s.DB.DeleteUserLobbies(ctx, *game.Player2ID)
	}

	p1Sunk := 0
	p2Sunk := 0
	for _, ship := range game.Ships {
		if ship.PlayerID == game.Player1ID && ship.Sunk {
			p1Sunk++
		} else if game.Player2ID != nil && ship.PlayerID == *game.Player2ID && ship.Sunk {
			p2Sunk++
		}
	}

	p1Shots, p1Hits := 0, 0
	p2Shots, p2Hits := 0, 0
	for _, move := range game.Moves {
		if move.PlayerID == game.Player1ID {
			p1Shots++
			if move.Hit {
				p1Hits++
			}
		} else if game.Player2ID != nil && move.PlayerID == *game.Player2ID {
			p2Shots++
			if move.Hit {
				p2Hits++
			}
		}
	}

	isDraw := result == "draw"
	winnerExists := winnerID != uuid.Nil && !isDraw

	s.DB.UpdateProfileStats(ctx, db.UpdateProfileStatsParams{
		UserID:     game.Player1ID,
		TotalGames: 1,
		Wins:       boolToInt32(winnerExists && winnerID == game.Player1ID),
		Losses:     boolToInt32(winnerExists && game.Player2ID != nil && winnerID != game.Player1ID),
		ShipsSunk:  int32(p2Sunk),
		TotalShots: int32(p1Shots),
		Hits:       int32(p1Hits),
	})
	if game.Player2ID != nil {
		s.DB.UpdateProfileStats(ctx, db.UpdateProfileStatsParams{
			UserID:     *game.Player2ID,
			TotalGames: 1,
			Wins:       boolToInt32(winnerExists && winnerID == *game.Player2ID),
			Losses:     boolToInt32(winnerExists && winnerID != *game.Player2ID),
			ShipsSunk:  int32(p1Sunk),
			TotalShots: int32(p2Shots),
			Hits:       int32(p2Hits),
		})
	}

	// --- Economy: calculate coin rewards ---
	var p1Result string
	var p2Result string
	if isDraw {
		p1Result = "DRAW"
		p2Result = "DRAW"
	} else if winnerExists && winnerID == game.Player1ID {
		p1Result = "WIN"
		p2Result = "LOSE"
	} else {
		p1Result = "LOSE"
		p2Result = "WIN"
	}

	perfectWin1 := winnerExists && winnerID == game.Player1ID && p1Sunk == 0
	perfectWin2 := winnerExists && game.Player2ID != nil && winnerID == *game.Player2ID && p2Sunk == 0

	reward1 := calcGameReward(p1Result, p1Hits, perfectWin1)
	reward2 := calcGameReward(p2Result, p2Hits, perfectWin2)

	earnedDelta1 := int32(0)
	if reward1 > 0 {
		earnedDelta1 = int32(reward1)
	}
	earnedDelta2 := int32(0)
	if reward2 > 0 {
		earnedDelta2 = int32(reward2)
	}

	s.DB.AddGameReward(ctx, game.Player1ID, int32(reward1), earnedDelta1)
	if game.Player2ID != nil {
		s.DB.AddGameReward(ctx, *game.Player2ID, int32(reward2), earnedDelta2)
	}

	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}

	winnerUsername := ""
	if winnerExists && winnerID == game.Player1ID {
		winnerUsername = game.Player1ID.String()
	} else if winnerExists && game.Player2ID != nil && winnerID == *game.Player2ID {
		winnerUsername = game.Player2ID.String()
	}
	winnerIDStr := ""
	if winnerExists {
		winnerIDStr = winnerID.String()
	}

	gameOverData := GameOverData{
		GameID:         gameID.String(),
		WinnerID:       winnerIDStr,
		WinnerUsername: winnerUsername,
		WinReason:      winReason,
		Player1Sunk:    p1Sunk,
		Player2Sunk:    p2Sunk,
		Result:         result,
		Reward1:        reward1,
		Reward2:        reward2,
		Hits1:          p1Hits,
		Hits2:          p2Hits,
		PerfectWin1:    perfectWin1,
		PerfectWin2:    perfectWin2,
	}

	room.mu.RLock()
	for _, c := range room.Clients {
		c.SendJSON(WSMessage{
			Type: "game_over",
			Data: mustJSON(gameOverData),
		})
	}
	room.mu.RUnlock()

	s.startGameOverTimer(gameID)
}

func calcGameReward(result string, hits int, perfectWin bool) int {
	var resultReward int
	switch result {
	case "WIN":
		resultReward = WIN_REWARD
	case "DRAW":
		resultReward = DRAW_REWARD
	case "LOSE":
		resultReward = LOSE_REWARD
	}

	baseResult := resultReward + hits*HIT_REWARD
	if perfectWin {
		baseResult += PERFECT_WIN_BONUS
	}

	randomFactor := RANDOM_FACTOR_MIN + rand.Float64()*(RANDOM_FACTOR_MAX-RANDOM_FACTOR_MIN)
	finalResult := int(math.Round(float64(baseResult) * (1 + randomFactor)))

	return finalResult
}

func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func lastMoveHit(game *GameRoom) bool {
	if len(game.Moves) == 0 {
		return false
	}
	return game.Moves[len(game.Moves)-1].Hit
}

func (s *Server) getUserIDFromToken(r *http.Request) (uuid.UUID, error) {
	tokenStr := ""

	// 1. Читаем из HttpOnly cookie
	if c, err := r.Cookie("auth_token"); err == nil && c.Value != "" {
		tokenStr = c.Value
	}

	// 2. Fallback: Authorization header (для совместимости с WS и старыми клиентами)
	if tokenStr == "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			parts := strings.Split(authHeader, " ")
			if len(parts) == 2 && parts[0] == "Bearer" {
				tokenStr = parts[1]
			}
		}
	}

	if tokenStr == "" {
		return uuid.Nil, errors.New("missing token")
	}

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.JWTKey, nil
	})
	if err != nil || !token.Valid {
		return uuid.Nil, errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return uuid.Nil, errors.New("invalid claims")
	}
	if tokenType, _ := claims["type"].(string); tokenType != "" && tokenType != "access" {
		return uuid.Nil, errors.New("not an access token")
	}
	userIDStr, ok := claims["sub"].(string)
	if !ok {
		return uuid.Nil, errors.New("user id not found in token")
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, errors.New("invalid user id format")
	}

	// Проверяем token_version, если есть в claims (только для полных токенов, не temp)
	if tv, hasVersion := claims["token_version"]; hasVersion {
		expectedVersion, ok := tv.(float64)
		if !ok {
			return uuid.Nil, errors.New("invalid token_version in claims")
		}
		user, err := s.DB.GetUserWithTokenVersion(r.Context(), userID)
		if err != nil {
			return uuid.Nil, errors.New("user not found")
		}
		if int32(expectedVersion) != user.TokenVersion {
			return uuid.Nil, errors.New("token revoked")
		}
	}

	return userID, nil
}

func (s *Server) parseTempToken(tokenStr string) (uuid.UUID, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
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
	n, err := crand.Int(crand.Reader, big.NewInt(1000000))
	if err != nil {
		return fmt.Sprintf("%06d", 0)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

func maskEmail(email string) string {
	at := strings.Index(email, "@")
	if at <= 1 {
		return "***" + email[at:]
	}
	return email[:2] + "***" + email[at:]
}

func (s *Server) sendEmail(to, code, subject string) error {
	if s.SMTP.Host == "" {
		log.Printf("[EMAIL DEBUG] To: %s, Code: %s", to, code)
		return nil
	}

	auth := smtp.PlainAuth("", s.SMTP.Username, s.SMTP.Password, s.SMTP.Host)
	msg := []byte("From: " + s.SMTP.From + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		"Ваш код: " + code + "\r\n" +
		"Код действителен 5 минут.\r\n")

	addr := s.SMTP.Host + ":587"
	err := smtp.SendMail(addr, auth, s.SMTP.From, []string{to}, msg)
	if err != nil {
		log.Printf("[SMTP ERROR] to=%s subject=%s: %v", to, subject, err)
	}
	return err
}

func (s *Server) checkCodeSendRateLimit(key string) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	last, ok := s.codeRateLimits[key]
	if ok && time.Since(last) < codeSendCooldown {
		return false
	}
	s.codeRateLimits[key] = time.Now()
	return true
}

func (s *Server) storeCode(key, code string) {
	s.codesMu.Lock()
	defer s.codesMu.Unlock()
	s.codes[key] = otpEntry{
		Code:      code,
		ExpiresAt: time.Now().Add(otpExpiry),
	}
}

func (s *Server) verifyCode(key, code string) (success, blocked bool) {
	s.codesMu.Lock()
	defer s.codesMu.Unlock()
	entry, ok := s.codes[key]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return false, false
	}
	if entry.Attempts >= maxCodeAttempts {
		return false, true
	}
	if entry.Code != code {
		entry.Attempts++
		s.codes[key] = entry
		return false, false
	}
	delete(s.codes, key)
	return true, false
}

func genInviteCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	code := make([]byte, 8)
	for i := range code {
		n, err := crand.Int(crand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			code[i] = chars[0]
		} else {
			code[i] = chars[n.Int64()]
		}
	}
	return string(code)
}

func profileToMap(p db.Profile) map[string]interface{} {
	var winPct, hitPct float64
	if p.TotalGames > 0 {
		winPct = float64(p.Wins) / float64(p.TotalGames) * 100
	}
	if p.TotalShots > 0 {
		hitPct = float64(p.Hits) / float64(p.TotalShots) * 100
	}
	return map[string]interface{}{
		"total_games":    p.TotalGames,
		"wins":           p.Wins,
		"losses":         p.Losses,
		"ships_sunk":     p.ShipsSunk,
		"total_shots":    p.TotalShots,
		"hits":           p.Hits,
		"win_percentage": winPct,
		"hit_percentage": hitPct,
	}
}

// ---------- Вспомогательное для генерации кораблей ----------

func generateSimpleShips() []Ship {
	var result []Ship
	shipDefs := map[int]int{4: 1, 3: 2, 2: 3, 1: 4}

	// Простой fallback — корабли "лесенкой"
	occupied := make([][]bool, boardSize)
	for i := range occupied {
		occupied[i] = make([]bool, boardSize)
	}

	for size, count := range shipDefs {
		for k := 0; k < count; k++ {
			placed := false
			for y := 0; y < boardSize && !placed; y++ {
				for x := 0; x < boardSize && !placed; x++ {
					horizontal := rand.Intn(2) == 0
					if horizontal && x+size > boardSize {
						continue
					}
					if !horizontal && y+size > boardSize {
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
								if nx >= 0 && nx < boardSize && ny >= 0 && ny < boardSize && occupied[ny][nx] {
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
