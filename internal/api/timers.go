package api

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	PlacementTimerDuration = 90
	TurnTimerDuration      = 20
	timerTickInterval      = 1
)

type TimerManager struct {
	mu        sync.Mutex
	placement map[uuid.UUID]context.CancelFunc
	turns     map[uuid.UUID]context.CancelFunc
}

func NewTimerManager() *TimerManager {
	return &TimerManager{
		placement: make(map[uuid.UUID]context.CancelFunc),
		turns:     make(map[uuid.UUID]context.CancelFunc),
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

func (tm *TimerManager) StopAll(gameID uuid.UUID) {
	tm.StopPlacement(gameID)
	tm.StopTurn(gameID)
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

func (s *Server) handlePlacementTimeout(gameID uuid.UUID) {
	s.Timers.StopPlacement(gameID)

	game, ok := s.Games.Get(gameID)
	if !ok || game.Status != "placing_ships" {
		return
	}

	p1Ships := countPlayerShips(game, game.Player1ID)
	p2Ships := 0
	if game.Player2ID != nil {
		p2Ships = countPlayerShips(game, *game.Player2ID)
	}

	room := s.Hub.GetRoom(gameID)

	if p1Ships < 10 {
		p1Ships := generateSimpleShips()
		for i := range p1Ships {
			p1Ships[i].ID = uuid.New()
			p1Ships[i].PlayerID = game.Player1ID
			p1Ships[i].Cells = buildCells(p1Ships[i].StartX, p1Ships[i].StartY, p1Ships[i].ShipType, p1Ships[i].Horizontal)
		}
		game.Ships = append(game.Ships, p1Ships...)
		if room != nil {
			for _, c := range room.Clients {
				if c.UserID == game.Player1ID {
					c.SendJSON(WSMessage{
						Type: "placement_auto_filled",
						Data: mustJSON(map[string]string{
							"message": "Ваше время вышло, корабли расставлены случайно",
						}),
					})
				}
			}
		}
	}

	if game.Player2ID != nil && p2Ships < 10 {
		p2Ships := generateSimpleShips()
		for i := range p2Ships {
			p2Ships[i].ID = uuid.New()
			p2Ships[i].PlayerID = *game.Player2ID
			p2Ships[i].Cells = buildCells(p2Ships[i].StartX, p2Ships[i].StartY, p2Ships[i].ShipType, p2Ships[i].Horizontal)
		}
		game.Ships = append(game.Ships, p2Ships...)
		if room != nil {
			for _, c := range room.Clients {
				if c.UserID == *game.Player2ID {
					c.SendJSON(WSMessage{
						Type: "placement_auto_filled",
						Data: mustJSON(map[string]string{
							"message": "Ваше время вышло, корабли расставлены случайно",
						}),
					})
				}
			}
		}
	}

	game.Status = "playing"
	game.CurrentTurn = &game.Player1ID
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
	var nextPlayer uuid.UUID
	if currentPlayer == game.Player1ID {
		if game.Player2ID == nil {
			return
		}
		nextPlayer = *game.Player2ID
	} else {
		nextPlayer = game.Player1ID
	}
	game.CurrentTurn = &nextPlayer
	s.Games.db.SetGameCurrentTurn(s.Games.ctx, gameID, game.CurrentTurn)

	room := s.Hub.GetRoom(gameID)
	if room != nil {
		room.mu.RLock()
		for _, c := range room.Clients {
			if c.UserID == currentPlayer {
				c.SendJSON(WSMessage{
					Type: "turn_timeout",
					Data: mustJSON(map[string]string{
						"message": "Время вышло! Ход переходит сопернику.",
					}),
				})
			} else {
				c.SendJSON(WSMessage{
					Type: "turn_timeout",
					Data: mustJSON(map[string]string{
						"message": "Соперник не сделал ход вовремя. Ваша очередь!",
					}),
				})
			}
		}
		room.mu.RUnlock()
	}

	s.broadcastGameState(gameID)
	s.broadcastYourTurn(gameID, nextPlayer)
	s.startTurnTimer(gameID)
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

func (s *Server) handleLeaveLobby(c *Client) {
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

	game, ok := s.Games.Get(gameID)
	if ok {
		game.Status = "finished"
		s.Games.db.SetGameStatus(s.Games.ctx, gameID, "finished")
	}

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
	game, ok := s.Games.Get(gameID)
	if !ok {
		return
	}

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

	if game.Status == "playing" {
		var winnerID uuid.UUID
		if game.Player1ID == c.UserID {
			if game.Player2ID != nil {
				winnerID = *game.Player2ID
			}
		} else {
			winnerID = game.Player1ID
		}
		if winnerID != uuid.Nil {
			game.Status = "finished"
			game.WinnerID = &winnerID
			s.broadcastGameOver(gameID, winnerID, "disconnect")
		}
	} else {
		game.Status = "finished"
		s.Games.db.SetGameStatus(s.Games.ctx, gameID, "finished")
	}
}
