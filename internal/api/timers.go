package api

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	PlacementTimerDuration = 90
	TurnTimerDuration      = 25
	GameOverTimerDuration  = 20
	timerTickInterval      = 1
)

type TimerManager struct {
	mu        sync.Mutex
	placement map[uuid.UUID]context.CancelFunc
	turns     map[uuid.UUID]context.CancelFunc
	gameOver  map[uuid.UUID]context.CancelFunc
}

func NewTimerManager() *TimerManager {
	return &TimerManager{
		placement: make(map[uuid.UUID]context.CancelFunc),
		turns:     make(map[uuid.UUID]context.CancelFunc),
		gameOver:  make(map[uuid.UUID]context.CancelFunc),
	}
}

func (tm *TimerManager) StopPlacement(gameID uuid.UUID) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if cancel, ok := tm.placement[gameID]; ok {
		cancel()
		delete(tm.placement, gameID)
	}
}

func (tm *TimerManager) StopTurn(gameID uuid.UUID) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if cancel, ok := tm.turns[gameID]; ok {
		cancel()
		delete(tm.turns, gameID)
	}
}

func (tm *TimerManager) StopGameOver(gameID uuid.UUID) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if cancel, ok := tm.gameOver[gameID]; ok {
		cancel()
		delete(tm.gameOver, gameID)
	}
}

func (tm *TimerManager) StopAll(gameID uuid.UUID) {
	tm.StopPlacement(gameID)
	tm.StopTurn(gameID)
	tm.StopGameOver(gameID)
}

func (s *Server) startPlacementTimer(gameID uuid.UUID) {
	s.Timers.StopPlacement(gameID)

	ctx, cancel := context.WithCancel(context.Background())
	s.Timers.mu.Lock()
	s.Timers.placement[gameID] = cancel
	s.Timers.mu.Unlock()

	go func() {
		secondsLeft := PlacementTimerDuration
		ticker := time.NewTicker(timerTickInterval * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				secondsLeft--
				if secondsLeft <= 0 {
					s.handlePlacementTimeout(gameID)
					return
				}
				s.broadcastTimerTick(gameID, "placement", secondsLeft, uuid.Nil)
			}
		}
	}()
}

func (s *Server) startTurnTimer(gameID uuid.UUID) {
	s.Timers.StopTurn(gameID)

	ctx, cancel := context.WithCancel(context.Background())
	s.Timers.mu.Lock()
	s.Timers.turns[gameID] = cancel
	s.Timers.mu.Unlock()

	go func() {
		secondsLeft := TurnTimerDuration
		ticker := time.NewTicker(timerTickInterval * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				secondsLeft--
				if secondsLeft <= 0 {
					s.handleTurnTimeout(gameID)
					return
				}
				game, ok := s.Games.Get(gameID)
				currentTurn := uuid.Nil
				if ok && game.CurrentTurn != nil {
					currentTurn = *game.CurrentTurn
				}
				s.broadcastTimerTick(gameID, "turn", secondsLeft, currentTurn)
			}
		}
	}()
}

func (s *Server) startGameOverTimer(gameID uuid.UUID) {
	s.Timers.StopGameOver(gameID)

	ctx, cancel := context.WithCancel(context.Background())
	s.Timers.mu.Lock()
	s.Timers.gameOver[gameID] = cancel
	s.Timers.mu.Unlock()

	go func() {
		secondsLeft := GameOverTimerDuration
		ticker := time.NewTicker(timerTickInterval * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				secondsLeft--
				if secondsLeft <= 0 {
					s.handleGameOverTimeout(gameID)
					return
				}
				s.broadcastTimerTick(gameID, "gameover", secondsLeft, uuid.Nil)
			}
		}
	}()
}

func (s *Server) handleGameOverTimeout(gameID uuid.UUID) {
	s.Timers.StopGameOver(gameID)

	s.Games.Lock()
	if g, ok := s.Games.GetLocked(gameID); ok {
		g.IsRevanchReady1 = false
		g.IsRevanchReady2 = false
	}
	s.Games.Unlock()

	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}

	room.mu.RLock()
	clients := make([]*Client, 0, len(room.Clients))
	for _, c := range room.Clients {
		clients = append(clients, c)
	}
	room.mu.RUnlock()

	for _, c := range clients {
		c.SendJSON(WSMessage{
			Type: "force_leave_to_lobby",
			Data: mustJSON(map[string]string{
				"message": "Время вышло",
			}),
		})
		c.Room = nil
	}
}

func (s *Server) handlePlacementTimeout(gameID uuid.UUID) {
	s.Timers.StopPlacement(gameID)

	s.Games.Lock()
	game, ok := s.Games.GetLocked(gameID)
	if !ok || game.Status != "placing_ships" {
		s.Games.Unlock()
		return
	}

	p1Ships := countPlayerShips(game, game.Player1ID)
	p2Ships := 0
	if game.Player2ID != nil {
		p2Ships = countPlayerShips(game, *game.Player2ID)
	}

	notifyP1 := false
	notifyP2 := false

	if p1Ships < 10 {
		generated := generateSimpleShips()
		for i := range generated {
			generated[i].ID = uuid.New()
			generated[i].PlayerID = game.Player1ID
			generated[i].Cells = buildCells(generated[i].StartX, generated[i].StartY, generated[i].ShipType, generated[i].Horizontal)
		}
		game.Ships = append(game.Ships, generated...)
		notifyP1 = true
	}

	if game.Player2ID != nil && p2Ships < 10 {
		generated := generateSimpleShips()
		for i := range generated {
			generated[i].ID = uuid.New()
			generated[i].PlayerID = *game.Player2ID
			generated[i].Cells = buildCells(generated[i].StartX, generated[i].StartY, generated[i].ShipType, generated[i].Horizontal)
		}
		game.Ships = append(game.Ships, generated...)
		notifyP2 = true
	}

	game.Status = "playing"
	game.CurrentTurn = &game.Player1ID
	s.Games.Unlock()

	room := s.Hub.GetRoom(gameID)
	if room != nil {
		room.mu.RLock()
		for _, c := range room.Clients {
			if (notifyP1 && c.UserID == game.Player1ID) || (notifyP2 && game.Player2ID != nil && c.UserID == *game.Player2ID) {
				c.SendJSON(WSMessage{
					Type: "placement_auto_filled",
					Data: mustJSON(map[string]string{
						"message": "Ваше время вышло, корабли расставлены случайно",
					}),
				})
			}
		}
		room.mu.RUnlock()
	}

	s.Games.db.SetGameStatus(s.Games.ctx, gameID, "playing")
	s.Games.db.SetGameCurrentTurn(s.Games.ctx, gameID, game.CurrentTurn)

	s.broadcastGameStarted(gameID)
	s.startTurnTimer(gameID)
}

func (s *Server) handleTurnTimeout(gameID uuid.UUID) {
	s.Timers.StopTurn(gameID)

	game, ok := s.Games.Get(gameID)
	if !ok || game.Status != "playing" {
		return
	}
	if game.CurrentTurn == nil {
		return
	}

	currentPlayer := *game.CurrentTurn

	if game.Player2ID == nil {
		return
	}

	// Collect all cells the current player has already shot at
	shot := make(map[[2]int]bool)
	for _, m := range game.Moves {
		if m.PlayerID == currentPlayer {
			shot[[2]int{m.X, m.Y}] = true
		}
	}

	// Build list of available cells (all 10x10 board minus shot cells)
	var available [][2]int
	for x := 0; x < 10; x++ {
		for y := 0; y < 10; y++ {
			if !shot[[2]int{x, y}] {
				available = append(available, [2]int{x, y})
			}
		}
	}

	if len(available) == 0 {
		return
	}

	cell := available[rand.Intn(len(available))]

	// Send turn_timeout notification to both players
	room := s.Hub.GetRoom(gameID)
	if room != nil {
		room.mu.RLock()
		for _, c := range room.Clients {
			if c.UserID == currentPlayer {
				c.SendJSON(WSMessage{
					Type: "turn_timeout",
					Data: mustJSON(map[string]string{
						"message": "Время вышло! Ход выполняется автоматически.",
					}),
				})
			} else {
				c.SendJSON(WSMessage{
					Type: "turn_timeout",
					Data: mustJSON(map[string]string{
						"message": "Соперник не сделал ход вовремя. Ход выполняется автоматически.",
					}),
				})
			}
		}
		room.mu.RUnlock()
	}

	// Execute the random move
	var errMsg string
	game, errMsg = s.Games.MakeMove(gameID, currentPlayer, cell[0], cell[1])
	if errMsg != "" {
		// MakeMove failed (race with player's own move) — force turn switch
		s.Games.Lock()
		game, ok = s.Games.GetLocked(gameID)
		if ok && game.Status == "playing" && game.CurrentTurn != nil && *game.CurrentTurn == currentPlayer {
			var nextPlayer uuid.UUID
			if currentPlayer == game.Player1ID {
				nextPlayer = *game.Player2ID
			} else {
				nextPlayer = game.Player1ID
			}
			game.CurrentTurn = &nextPlayer
			s.Games.Unlock()
			s.Games.db.SetGameCurrentTurn(s.Games.ctx, gameID, game.CurrentTurn)
			s.broadcastGameState(gameID)
			s.broadcastYourTurn(gameID, nextPlayer)
			s.startTurnTimer(gameID)
		} else {
			s.Games.Unlock()
		}
		return
	}

	// Broadcast results (same pattern as MakeMove HTTP handler)
	s.broadcastGameState(gameID)
	s.broadcastOpponentMoved(gameID, currentPlayer, cell[0], cell[1], game)
	if game.Status == "finished" {
		s.Timers.StopAll(gameID)
		result := "win"
		winnerID := uuid.Nil
		if game.WinnerID != nil {
			winnerID = *game.WinnerID
		} else {
			result = "draw"
		}
		s.broadcastGameOver(gameID, winnerID, "all_ships_sunk", result)
	} else {
		s.broadcastYourTurn(gameID, *game.CurrentTurn)
		s.startTurnTimer(gameID)
	}
}

func (s *Server) broadcastTimerTick(gameID uuid.UUID, timerType string, secondsLeft int, currentTurn uuid.UUID) {
	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}
	currentTurnStr := ""
	if currentTurn != uuid.Nil {
		currentTurnStr = currentTurn.String()
	}
	msg := WSMessage{
		Type: "timer_tick",
		Data: mustJSON(map[string]interface{}{
			"timer_type":   timerType,
			"seconds_left": secondsLeft,
			"current_turn": currentTurnStr,
		}),
	}
	room.mu.RLock()
	for _, c := range room.Clients {
		c.SendJSON(msg)
	}
	room.mu.RUnlock()
}

func countPlayerShips(game *GameRoom, playerID uuid.UUID) int {
	count := 0
	for _, s := range game.Ships {
		if s.PlayerID == playerID {
			count++
		}
	}
	return count
}

func (s *Server) handleForceLeaveToLobby(c *Client) {
	if c.Room == nil {
		c.SendJSON(WSMessage{Type: "left_lobby"})
		return
	}

	gameID := c.Room.GameID

	otherClient := c.Room.GetOtherClient(c.UserID)
	if otherClient != nil {
		otherClient.SendJSON(WSMessage{
			Type: "opponent_left",
			Data: mustJSON(map[string]string{
				"message": "Соперник покинул игру",
			}),
		})
	}

	c.Room.RemoveClient(c.UserID)
	c.Room = nil

	s.Timers.StopGameOver(gameID)

	c.SendJSON(WSMessage{
		Type: "force_leave_to_lobby",
		Data: mustJSON(map[string]string{
			"message": "Вы покинули игру",
		}),
	})

	s.Games.Lock()
	if game, ok := s.Games.GetLocked(gameID); ok {
		game.IsRevanchReady1 = false
		game.IsRevanchReady2 = false
	}
	s.Games.Unlock()
}

func (s *Server) handleToggleRevanch(c *Client) {
	if c.Room == nil {
		return
	}

	gameID := c.Room.GameID

	s.Games.Lock()
	game, ok := s.Games.GetLocked(gameID)
	if !ok || game.Status != "finished" {
		s.Games.Unlock()
		return
	}

	if c.UserID == game.Player1ID {
		game.IsRevanchReady1 = !game.IsRevanchReady1
	} else if game.Player2ID != nil && c.UserID == *game.Player2ID {
		game.IsRevanchReady2 = !game.IsRevanchReady2
	} else {
		s.Games.Unlock()
		return
	}

	ready1 := game.IsRevanchReady1
	ready2 := game.IsRevanchReady2
	s.Games.Unlock()

	room := s.Hub.GetRoom(gameID)
	if room == nil {
		return
	}

	revanchData := map[string]interface{}{
		"player1_ready": ready1,
		"player2_ready": ready2,
	}

	room.mu.RLock()
	for _, client := range room.Clients {
		client.SendJSON(WSMessage{
			Type: "revanch_toggle",
			Data: mustJSON(revanchData),
		})
	}
	room.mu.RUnlock()

	if ready1 && ready2 {
		s.startRematch(gameID)
	}
}

func (s *Server) startRematch(oldGameID uuid.UUID) {
	s.Games.Lock()
	oldGame, ok := s.Games.GetLocked(oldGameID)
	if !ok || oldGame.Player2ID == nil {
		s.Games.Unlock()
		return
	}

	p1 := oldGame.Player1ID
	p2 := *oldGame.Player2ID

	// Reset revanch flags immediately to prevent double startRematch
	oldGame.IsRevanchReady1 = false
	oldGame.IsRevanchReady2 = false
	s.Games.Unlock()

	s.Timers.StopGameOver(oldGameID)

	// Create new game directly — DON'T use CreateGameSession because it
	// would add the old (soon-to-disconnect) clients to the new room,
	// causing handleClientDisconnect to corrupt the new game.
	newGame := s.Games.Create(p1)
	s.Games.Join(newGame.ID, p2)
	s.startPlacementTimer(newGame.ID)

	log.Printf("[REMATCH] new game %s for players %s, %s", newGame.ID, p1, p2)

	// Create the new room (empty, clients will join on reconnect)
	s.Hub.GetOrCreateRoom(newGame.ID)

	// Notify old clients and detach them so their eventual WS close
	// won't affect the new game
	room := s.Hub.GetRoom(oldGameID)
	if room != nil {
		room.mu.RLock()
		clients := make([]*Client, 0, len(room.Clients))
		for _, c := range room.Clients {
			clients = append(clients, c)
		}
		room.mu.RUnlock()

		for _, c := range clients {
			c.SendJSON(WSMessage{
				Type: "revanch_game_start",
				Data: mustJSON(map[string]string{
					"game_id":     newGame.ID.String(),
					"new_game_id": newGame.ID.String(),
				}),
			})
		}

		// Detach AFTER sending so handleClientDisconnect returns early
		for _, c := range clients {
			c.Room = nil
		}
	}

	s.Hub.mu.Lock()
	delete(s.Hub.rooms, oldGameID)
	s.Hub.mu.Unlock()
}

func (s *Server) handleLeaveLobby(c *Client) {
	s.DB.LeaveMatchmaking(context.Background(), c.UserID)

	if c.Room == nil {
		c.SendJSON(WSMessage{Type: "left_lobby"})
		return
	}

	gameID := c.Room.GameID

	otherClient := c.Room.GetOtherClient(c.UserID)
	if otherClient != nil {
		otherClient.SendJSON(WSMessage{
			Type: "opponent_left",
			Data: mustJSON(map[string]string{
				"message": "Соперник покинул игру",
			}),
		})
	}

	c.Room.RemoveClient(c.UserID)
	c.Room = nil

	s.Timers.StopAll(gameID)

	s.Games.Lock()
	if game, ok := s.Games.GetLocked(gameID); ok {
		game.Status = "finished"
		game.IsRevanchReady1 = false
		game.IsRevanchReady2 = false
	}
	s.Games.Unlock()

	s.Games.db.SetGameStatus(s.Games.ctx, gameID, "finished")

	s.Hub.mu.Lock()
	delete(s.Hub.rooms, gameID)
	s.Hub.mu.Unlock()

	c.SendJSON(WSMessage{Type: "left_lobby"})
}

func (s *Server) handleClientDisconnect(c *Client) {
	if c.Room == nil {
		return
	}

	gameID := c.Room.GameID

	s.Games.Lock()
	game, ok := s.Games.GetLocked(gameID)
	if !ok {
		s.Games.Unlock()
		return
	}

	gameStatus := game.Status

	var winnerID uuid.UUID
	shouldBroadcastGameOver := false
	shouldFinish := false

	if gameStatus == "playing" {
		if game.Player1ID == c.UserID && game.Player2ID != nil {
			winnerID = *game.Player2ID
		} else if game.Player1ID != c.UserID {
			winnerID = game.Player1ID
		}
		if winnerID != uuid.Nil {
			game.Status = "finished"
			game.WinnerID = &winnerID
			shouldBroadcastGameOver = true
		}
	} else {
		game.Status = "finished"
		shouldFinish = true
	}
	s.Games.Unlock()

	s.Timers.StopAll(gameID)

	otherClient := c.Room.GetOtherClient(c.UserID)
	if otherClient != nil {
		otherClient.SendJSON(WSMessage{
			Type: "opponent_left",
			Data: mustJSON(map[string]string{
				"message": "Соперник потерял соединение",
			}),
		})
	}

	if shouldFinish {
		s.Games.db.SetGameStatus(s.Games.ctx, gameID, "finished")
	}
	if shouldBroadcastGameOver && winnerID != uuid.Nil {
		s.broadcastGameOver(gameID, winnerID, "disconnect", "win")
	}
}
