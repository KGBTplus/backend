package api

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/google/uuid"
)

type Cell struct {
	X   int  `json:"x"`
	Y   int  `json:"y"`
	Hit bool `json:"hit"`
}

type Ship struct {
	ID         uuid.UUID `json:"id"`
	PlayerID   uuid.UUID `json:"player_id"`
	ShipType   int       `json:"ship_type"`
	StartX     int       `json:"start_x"`
	StartY     int       `json:"start_y"`
	Horizontal bool      `json:"horizontal"`
	Cells      []Cell    `json:"cells"`
	Sunk       bool      `json:"sunk"`
	DBID       *uuid.UUID `json:"-"`
}

type Move struct {
	ID         uuid.UUID  `json:"id"`
	PlayerID   uuid.UUID  `json:"player_id"`
	X          int        `json:"x"`
	Y          int        `json:"y"`
	Hit        bool       `json:"hit"`
	SunkShipID *uuid.UUID `json:"sunk_ship_id,omitempty"`
}

type GameRoom struct {
	ID          uuid.UUID  `json:"id"`
	Player1ID   uuid.UUID  `json:"player1_id"`
	Player2ID   *uuid.UUID `json:"player2_id,omitempty"`
	Status      string     `json:"status"`
	CurrentTurn *uuid.UUID `json:"current_turn,omitempty"`
	WinnerID    *uuid.UUID `json:"winner_id,omitempty"`
	Ships       []Ship     `json:"ships"`
	Moves       []Move     `json:"moves"`
	CreatedAt   time.Time  `json:"created_at"`
	BattleStartedAt *time.Time `json:"-"`
	FinalRoundTrigger *uuid.UUID `json:"-"`

	IsRevanchReady1  bool `json:"-"`
	IsRevanchReady2  bool `json:"-"`
	RematchInProgress bool `json:"-"`

	IsPlacingReady1 bool `json:"-"`
	IsPlacingReady2 bool `json:"-"`

	isGameOverBroadcasted bool
}

type GameStore struct {
	mu      sync.RWMutex
	games   map[uuid.UUID]*GameRoom
	db      *db.Queries
	ctx     context.Context
}

func NewGameStore(dbq *db.Queries) *GameStore {
	gs := &GameStore{
		games: make(map[uuid.UUID]*GameRoom),
		db:    dbq,
		ctx:   context.Background(),
	}
	gs.loadFromDB()
	return gs
}

func (gs *GameStore) loadFromDB() {
	rows, err := gs.db.GetAllActiveGames(gs.ctx)
	if err != nil {
		return
	}
	for _, r := range rows {
		game := &GameRoom{
			ID:          r.ID,
			Player1ID:   r.Player1ID,
			Player2ID:   r.Player2ID,
			Status:      r.Status,
			CurrentTurn: r.CurrentTurn,
			WinnerID:    r.WinnerID,
			CreatedAt:   r.CreatedAt,
			Ships:       []Ship{},
			Moves:       []Move{},
		}

		shipRows, _ := gs.db.GetGameShips(gs.ctx, r.ID)
		for _, sr := range shipRows {
			ship := Ship{
				ID:         uuid.New(),
				PlayerID:   sr.PlayerID,
				ShipType:   int(sr.ShipType),
				StartX:     int(sr.StartX),
				StartY:     int(sr.StartY),
				Horizontal: sr.Horizontal,
				Sunk:       sr.Sunk,
				Cells:      buildCells(int(sr.StartX), int(sr.StartY), int(sr.ShipType), sr.Horizontal),
			}
			dbID := sr.ID
			ship.DBID = &dbID
			game.Ships = append(game.Ships, ship)
		}

		moveRows, _ := gs.db.GetGameMoves(gs.ctx, r.ID)
		for _, mr := range moveRows {
			move := Move{
				ID:         mr.ID,
				PlayerID:   mr.PlayerID,
				X:          int(mr.X),
				Y:          int(mr.Y),
				Hit:        mr.Hit,
				SunkShipID: mr.SunkShipID,
			}
			game.Moves = append(game.Moves, move)
		}

		gs.games[r.ID] = game
	}
}

func (gs *GameStore) Create(playerID uuid.UUID) *GameRoom {
	g := &GameRoom{
		ID:        uuid.New(),
		Player1ID: playerID,
		Status:    "waiting",
		Ships:     []Ship{},
		Moves:     []Move{},
		CreatedAt: time.Now(),
	}
	gs.mu.Lock()
	gs.games[g.ID] = g
	gs.mu.Unlock()

	gs.db.CreateGameState(gs.ctx, db.CreateGameStateParams{
		ID:        g.ID,
		Player1ID: g.Player1ID,
		Status:    g.Status,
		CreatedAt: g.CreatedAt,
	})

	return g
}

func (gs *GameStore) Lock()    { gs.mu.Lock() }
func (gs *GameStore) Unlock()  { gs.mu.Unlock() }
func (gs *GameStore) RLock()   { gs.mu.RLock() }
func (gs *GameStore) RUnlock() { gs.mu.RUnlock() }

func (gs *GameStore) GetLocked(id uuid.UUID) (*GameRoom, bool) {
	g, ok := gs.games[id]
	return g, ok
}

func (gs *GameStore) Delete(id uuid.UUID) {
	gs.mu.Lock()
	delete(gs.games, id)
	gs.mu.Unlock()
}

func (gs *GameStore) CleanupStale() {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	now := time.Now()
	for id, g := range gs.games {
		if (g.Status == "waiting" || g.Status == "placing_ships") && now.Sub(g.CreatedAt) > 10*time.Minute {
			delete(gs.games, id)
		}
	}
}

func (gs *GameStore) Get(id uuid.UUID) (*GameRoom, bool) {
	gs.mu.RLock()
	g, ok := gs.games[id]
	gs.mu.RUnlock()
	return g, ok
}

func (gs *GameStore) ListAvailable(excludePlayerID uuid.UUID) []*GameRoom {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	var result []*GameRoom
	for _, g := range gs.games {
		if g.Status == "waiting" && g.Player1ID != excludePlayerID {
			result = append(result, g)
		}
	}
	return result
}

func (gs *GameStore) FindActiveGame(playerID uuid.UUID) *GameRoom {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	for _, g := range gs.games {
		if g.Status == "playing" || g.Status == "placing_ships" {
			if g.Player1ID == playerID || (g.Player2ID != nil && *g.Player2ID == playerID) {
				return g
			}
		}
	}
	return nil
}

func (gs *GameStore) Join(gameID, playerID uuid.UUID) bool {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	g, ok := gs.games[gameID]
	if !ok || g.Status != "waiting" {
		return false
	}
	g.Player2ID = &playerID
	g.Status = "placing_ships"
	g.CurrentTurn = &g.Player1ID

	gs.db.UpdateGameState(gs.ctx, db.UpdateGameStateParams{
		ID:          g.ID,
		Player2ID:   g.Player2ID,
		Status:      g.Status,
		CurrentTurn: g.CurrentTurn,
	})

	return true
}

func (gs *GameStore) DeletePlayerShips(gameID, playerID uuid.UUID) {
	gs.db.DeletePlayerShips(gs.ctx, gameID, playerID)
}

func validateShipPlacement(ships []Ship) bool {
	boardSize := 10
	occupied := make([][]bool, boardSize)
	for i := range occupied {
		occupied[i] = make([]bool, boardSize)
	}
	for _, s := range ships {
		if s.ShipType < 1 || s.ShipType > 4 {
			return false
		}
		if s.StartX < 0 || s.StartX >= boardSize || s.StartY < 0 || s.StartY >= boardSize {
			return false
		}
		var cells [][2]int
		for j := 0; j < s.ShipType; j++ {
			cx := s.StartX
			cy := s.StartY
			if s.Horizontal {
				cx += j
			} else {
				cy += j
			}
			if cx < 0 || cx >= boardSize || cy < 0 || cy >= boardSize {
				return false
			}
			cells = append(cells, [2]int{cx, cy})
		}
		for _, cell := range cells {
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := cell[0]+dx, cell[1]+dy
					if nx >= 0 && nx < boardSize && ny >= 0 && ny < boardSize {
						if occupied[ny][nx] {
							return false
						}
					}
				}
			}
		}
		for _, cell := range cells {
			occupied[cell[1]][cell[0]] = true
		}
	}

	typeCount := map[int]int{4: 1, 3: 2, 2: 3, 1: 4}
	for _, s := range ships {
		typeCount[s.ShipType]--
	}
	for _, c := range typeCount {
		if c != 0 {
			return false
		}
	}

	return true
}

func (gs *GameStore) PlaceShips(gameID, playerID uuid.UUID, ships []Ship) bool {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	g, ok := gs.games[gameID]
	if !ok {
		return false
	}
	if g.Status != "placing_ships" {
		return false
	}
	if g.Player1ID != playerID && (g.Player2ID == nil || *g.Player2ID != playerID) {
		return false
	}

	if !validateShipPlacement(ships) {
		return false
	}

	// Удаляем старые корабли игрока из БД
	gs.db.DeletePlayerShips(gs.ctx, gameID, playerID)
	var kept []Ship
	for _, s := range g.Ships {
		if s.PlayerID != playerID {
			kept = append(kept, s)
		}
	}

	for i := range ships {
		ships[i].ID = uuid.New()
		ships[i].PlayerID = playerID
		ships[i].Cells = buildCells(ships[i].StartX, ships[i].StartY, ships[i].ShipType, ships[i].Horizontal)
		ships[i].Sunk = false

		shipID := uuid.New()
		ships[i].DBID = &shipID
		gs.db.CreateShip(gs.ctx, db.CreateShipParams{
			ID:         shipID,
			GameID:     gameID,
			PlayerID:   playerID,
			ShipType:   int32(ships[i].ShipType),
			StartX:     int32(ships[i].StartX),
			StartY:     int32(ships[i].StartY),
			Horizontal: ships[i].Horizontal,
			Sunk:       false,
		})
	}

	g.Ships = append(kept, ships...)
	return true
}

func (gs *GameStore) All() []*GameRoom {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	result := make([]*GameRoom, 0, len(gs.games))
	for _, g := range gs.games {
		result = append(result, g)
	}
	return result
}

func (gs *GameStore) CheckAndStart(gameID uuid.UUID) {
	gs.mu.Lock()
	g, ok := gs.games[gameID]
	if !ok || g.Status != "placing_ships" || g.Player2ID == nil {
		gs.mu.Unlock()
		return
	}
	p1Ships := 0
	p2Ships := 0
	for _, s := range g.Ships {
		if s.PlayerID == g.Player1ID {
			p1Ships++
		} else if s.PlayerID == *g.Player2ID {
			p2Ships++
		}
	}
	if p1Ships >= 10 && p2Ships >= 10 && g.IsPlacingReady1 && g.IsPlacingReady2 {
		g.Status = "playing"
		if rand.Intn(2) == 0 {
			g.CurrentTurn = &g.Player1ID
		} else {
			g.CurrentTurn = g.Player2ID
		}
	}
	gs.mu.Unlock()

	if p1Ships >= 10 && p2Ships >= 10 && g.Status == "playing" {
		gs.db.SetGameStatus(gs.ctx, gameID, "playing")
		gs.db.SetGameCurrentTurn(gs.ctx, gameID, g.CurrentTurn)
	}
}

func (gs *GameStore) MakeMove(gameID, playerID uuid.UUID, x, y int) (*GameRoom, string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	g, ok := gs.games[gameID]
	if !ok {
		return nil, "Игра не найдена"
	}
	if g.Status != "playing" {
		return nil, "Игра не в статусе playing"
	}
	if g.CurrentTurn == nil || *g.CurrentTurn != playerID {
		return nil, "Сейчас не ваш ход"
	}

	for _, m := range g.Moves {
		if m.PlayerID == playerID && m.X == x && m.Y == y {
			return nil, "Эта клетка уже обстреляна"
		}
	}

	var opponentID uuid.UUID
	if playerID == g.Player1ID {
		opponentID = *g.Player2ID
	} else {
		opponentID = g.Player1ID
	}

	hit := false
	var sunkShipID *uuid.UUID
	var sunkDBID *uuid.UUID

	for i := range g.Ships {
		s := &g.Ships[i]
		if s.PlayerID != opponentID {
			continue
		}
		for j := range s.Cells {
			cell := &s.Cells[j]
			if cell.X == x && cell.Y == y && !cell.Hit {
				cell.Hit = true
				hit = true
				allHit := true
				for _, c := range s.Cells {
					if !c.Hit {
						allHit = false
						break
					}
				}
				if allHit {
					s.Sunk = true
					sunkShipID = &s.ID
					if s.DBID != nil {
						sunkDBID = s.DBID
					}
				}
				break
			}
		}
		if hit {
			break
		}
	}

	move := Move{
		ID:         uuid.New(),
		PlayerID:   playerID,
		X:          x,
		Y:          y,
		Hit:        hit,
		SunkShipID: sunkShipID,
	}
	g.Moves = append(g.Moves, move)

	gs.db.CreateMove(gs.ctx, db.CreateMoveParams{
		ID:         move.ID,
		GameID:     gameID,
		PlayerID:   playerID,
		X:          int32(x),
		Y:          int32(y),
		Hit:        hit,
		SunkShipID: sunkDBID,
	})

	if sunkDBID != nil {
		gs.db.SetShipSunk(gs.ctx, *sunkDBID)
	}

	allSunk := true
	for _, s := range g.Ships {
		if s.PlayerID == opponentID && !s.Sunk {
			allSunk = false
			break
		}
	}
	if allSunk {
		if g.FinalRoundTrigger == nil {
			trigger := playerID
			g.FinalRoundTrigger = &trigger
			g.CurrentTurn = &opponentID
			gs.db.SetGameCurrentTurn(gs.ctx, gameID, g.CurrentTurn)
			return g, ""
		}
		// Player sank opponent's last ship while in final round — check for draw
		otherAllSunk := true
		for _, s := range g.Ships {
			if s.PlayerID == playerID && !s.Sunk {
				otherAllSunk = false
				break
			}
		}
		if otherAllSunk {
			// Both players have all ships sunk — DRAW
			g.Status = "finished"
			g.WinnerID = nil
			gs.db.FinishGameState(gs.ctx, gameID, uuid.Nil)
			return g, ""
		}
		// Opponent was first to sink all, and player didn't sink all opponent's ships
		g.Status = "finished"
		g.WinnerID = g.FinalRoundTrigger
		gs.db.FinishGameState(gs.ctx, gameID, *g.FinalRoundTrigger)
		return g, ""
	}

	if g.FinalRoundTrigger != nil {
		// Final round: player didn't sink opponent's last ship, opponent wins
		g.Status = "finished"
		g.WinnerID = g.FinalRoundTrigger
		gs.db.FinishGameState(gs.ctx, gameID, *g.FinalRoundTrigger)
		return g, ""
	}

	if !hit {
		if playerID == g.Player1ID {
			g.CurrentTurn = g.Player2ID
		} else {
			g.CurrentTurn = &g.Player1ID
		}
		gs.db.SetGameCurrentTurn(gs.ctx, gameID, g.CurrentTurn)
	}

	return g, ""
}

func buildCells(startX, startY, shipType int, horizontal bool) []Cell {
	cells := make([]Cell, shipType)
	for i := 0; i < shipType; i++ {
		x := startX
		y := startY
		if horizontal {
			x += i
		} else {
			y += i
		}
		cells[i] = Cell{X: x, Y: y, Hit: false}
	}
	return cells
}

func (gs *GameStore) PlayerShips(g *GameRoom, playerID uuid.UUID) []Ship {
	var result []Ship
	for _, s := range g.Ships {
		if s.PlayerID == playerID {
			result = append(result, s)
		}
	}
	return result
}
