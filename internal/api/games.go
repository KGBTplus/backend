package api

import (
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

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

	page := 1
	limit := 20
	if params.Page != nil {
		page = *params.Page
	}
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit > 100 {
		limit = 100
	}
	offset := int32((page - 1) * limit)

	matches, err := s.DB.GetMatchHistory(r.Context(), userID, int32(limit), offset)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка загрузки истории")
		return
	}
	total, _ := s.DB.CountMatchHistory(r.Context(), userID)

	if matches == nil {
		matches = []db.MatchHistoryRow{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"matches": matches,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
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

	resp := s.gameToMap(r.Context(), game)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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

	s.Timers.StopAll(uuid.UUID(gameID))
	s.broadcastGameOver(uuid.UUID(gameID), winnerID, "forfeit", "win")

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

	s.broadcastGameState(uuid.UUID(gameID))
	s.broadcastOpponentMoved(uuid.UUID(gameID), userID, req.X, req.Y, game)
	if game.Status == "finished" {
		s.Timers.StopAll(uuid.UUID(gameID))
		result := "win"
		winnerID := uuid.Nil
		if game.WinnerID != nil {
			winnerID = *game.WinnerID
		} else {
			result = "draw"
		}
		s.broadcastGameOver(uuid.UUID(gameID), winnerID, "all_ships_sunk", result)
	} else {
		s.Timers.StopTurn(uuid.UUID(gameID))
		s.broadcastYourTurn(uuid.UUID(gameID), *game.CurrentTurn)
		s.startTurnTimer(uuid.UUID(gameID))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(game)
}

func filterShipsByPlayer(ships []Ship, playerID uuid.UUID) []Ship {
	var result []Ship
	for _, s := range ships {
		if s.PlayerID == playerID {
			result = append(result, s)
		}
	}
	return result
}

func (s *Server) broadcastGameState(gameID uuid.UUID) {
	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}
	game, ok := s.Games.Get(gameID)
	if !ok {
		return
	}
	room.mu.RLock()
	for _, c := range room.Clients {
		resp := s.gameToMap(context.Background(), game)
		resp["ships"] = filterShipsByPlayer(game.Ships, c.UserID)
		resp["my_user_id"] = c.UserID.String()
		c.SendJSON(resp)
	}
	room.mu.RUnlock()
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

	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body.Close()

	var req struct {
		Ships []struct {
			ShipType   int  `json:"ship_type"`
			StartX     int  `json:"start_x"`
			StartY     int  `json:"start_y"`
			Horizontal bool `json:"horizontal"`
		} `json:"ships"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if len(req.Ships) != maxShips {
		sendError(w, http.StatusBadRequest, "Должно быть ровно 10 кораблей")
		return
	}

	ships := make([]Ship, len(req.Ships))
	occupied := make([][]bool, boardSize)
	for i := range occupied {
		occupied[i] = make([]bool, boardSize)
	}

	for i, rs := range req.Ships {
		if rs.ShipType < 1 || rs.ShipType > 4 {
			sendError(w, http.StatusBadRequest, "Некорректный тип корабля")
			return
		}
		if rs.StartX < 0 || rs.StartX >= boardSize || rs.StartY < 0 || rs.StartY >= boardSize {
			sendError(w, http.StatusBadRequest, "Корабль за пределами поля")
			return
		}

		cells := make([][2]int, 0, rs.ShipType)
		for j := 0; j < rs.ShipType; j++ {
			cx := rs.StartX
			cy := rs.StartY
			if rs.Horizontal {
				cx += j
			} else {
				cy += j
			}
			if cx < 0 || cx >= boardSize || cy < 0 || cy >= boardSize {
				sendError(w, http.StatusBadRequest, "Корабль за пределами поля")
				return
			}
			if occupied[cy][cx] {
				sendError(w, http.StatusBadRequest, "Корабли пересекаются")
				return
			}
			cells = append(cells, [2]int{cx, cy})
		}

		// Проверка зазора в 1 клетку между кораблями
		for _, cell := range cells {
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := cell[0]+dx, cell[1]+dy
					if nx >= 0 && nx < boardSize && ny >= 0 && ny < boardSize {
						if occupied[ny][nx] {
							sendError(w, http.StatusBadRequest, "Корабли соприкасаются — необходим зазор в 1 клетку")
							return
						}
					}
				}
			}
		}

		for _, cell := range cells {
			occupied[cell[1]][cell[0]] = true
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

	game, _ := s.Games.Get(uuid.UUID(gameID))
	s.broadcastOpponentShipsPlaced(uuid.UUID(gameID), userID)
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

	// Mark player as ready
	if userID == game.Player1ID {
		game.IsPlacingReady1 = true
	} else if game.Player2ID != nil && userID == *game.Player2ID {
		game.IsPlacingReady2 = true
	}

	beforeStatus := game.Status
	s.Games.CheckAndStart(uuid.UUID(gameID))
	gameAfter, _ := s.Games.Get(uuid.UUID(gameID))

	if beforeStatus != "playing" && gameAfter.Status == "playing" {
		now := time.Now()
		gameAfter.BattleStartedAt = &now
	}

	s.broadcastOpponentReady(uuid.UUID(gameID), userID)
	if beforeStatus != "playing" && gameAfter.Status == "playing" {
		s.Timers.StopPlacement(uuid.UUID(gameID))
		s.broadcastGameStarted(uuid.UUID(gameID))
		s.broadcastYourTurn(uuid.UUID(gameID), *gameAfter.CurrentTurn)
		s.startTurnTimer(uuid.UUID(gameID))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(gameAfter)
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

	shipDefs := map[int]int{4: 1, 3: 2, 2: 3, 1: 4}
	var randomShips []Ship

	for size, count := range shipDefs {
		for k := 0; k < count; k++ {
			for attempts := 0; attempts < 1000; attempts++ {
				horizontal := rand.Intn(2) == 0
				startX := rand.Intn(boardSize)
				startY := rand.Intn(boardSize)
				if horizontal && startX+size > boardSize {
					continue
				}
				if !horizontal && startY+size > boardSize {
					continue
				}

				conflict := false
				occupied := make([][]bool, boardSize)
				for i := range occupied {
					occupied[i] = make([]bool, boardSize)
				}
				for _, existing := range randomShips {
					for j := 0; j < existing.ShipType; j++ {
						ex := existing.StartX
						ey := existing.StartY
						if existing.Horizontal {
							ex += j
						} else {
							ey += j
						}
						for dy := -1; dy <= 1; dy++ {
							for dx := -1; dx <= 1; dx++ {
								nx, ny := ex+dx, ey+dy
								if nx >= 0 && nx < boardSize && ny >= 0 && ny < boardSize {
									occupied[ny][nx] = true
								}
							}
						}
					}
				}
				for j := 0; j < size; j++ {
					cx := startX
					cy := startY
					if horizontal {
						cx += j
					} else {
						cy += j
					}
					if occupied[cy][cx] {
						conflict = true
						break
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

	if len(randomShips) != maxShips {
		randomShips = generateSimpleShips()
	}

	ok = s.Games.PlaceShips(uuid.UUID(gameID), userID, randomShips)
	if !ok {
		sendError(w, http.StatusBadRequest, "Не удалось расставить корабли")
		return
	}

	game, _ = s.Games.Get(uuid.UUID(gameID))
	s.broadcastOpponentShipsPlaced(uuid.UUID(gameID), userID)
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

	s.Games.DeletePlayerShips(uuid.UUID(gameID), userID)

	var kept []Ship
	for _, s := range game.Ships {
		if s.PlayerID != userID {
			kept = append(kept, s)
		}
	}
	game.Ships = kept

	// Reset ready flag
	if userID == game.Player1ID {
		game.IsPlacingReady1 = false
	} else if game.Player2ID != nil && userID == *game.Player2ID {
		game.IsPlacingReady2 = false
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(game)
}

func (s *Server) gameToMap(ctx context.Context, g *GameRoom) map[string]interface{} {
	m := map[string]interface{}{
		"id":           g.ID,
		"player1_id":   g.Player1ID,
		"player2_id":   g.Player2ID,
		"status":       g.Status,
		"current_turn": g.CurrentTurn,
		"winner_id":    g.WinnerID,
		"moves":        g.Moves,
		"created_at":   g.CreatedAt,
	}
	if p1, err := s.DB.GetUserByID(ctx, g.Player1ID); err == nil {
		m["player1_name"] = p1.Username
	}
	if p1p, err := s.DB.GetProfile(ctx, g.Player1ID); err == nil {
		m["player1_stats"] = profileToMap(p1p)
	}
	if g.Player2ID != nil {
		if p2, err := s.DB.GetUserByID(ctx, *g.Player2ID); err == nil {
			m["player2_name"] = p2.Username
		}
		if p2p, err := s.DB.GetProfile(ctx, *g.Player2ID); err == nil {
			m["player2_stats"] = profileToMap(p2p)
		}
	}
	return m
}
