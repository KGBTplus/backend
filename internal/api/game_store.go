package api

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/google/uuid"
)

var BotID = uuid.MustParse("00000000-0000-0000-0000-0000000000b0t")

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
}

type GameStore struct {
	mu    sync.RWMutex
	games map[uuid.UUID]*GameRoom
	db    *db.Queries
}

func NewGameStore(database *db.Queries) *GameStore {
	return &GameStore{
		games: make(map[uuid.UUID]*GameRoom),
		db:    database,
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

	if gs.db != nil {
		gs.db.CreateGame(context.Background(), db.CreateGameParams{
			ID:        g.ID,
			Player1ID: g.Player1ID,
			Status:    g.Status,
			CreatedAt: g.CreatedAt,
		})
	}
	return g
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

func (gs *GameStore) Join(gameID, playerID uuid.UUID) bool {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	g, ok := gs.games[gameID]
	if !ok || g.Status != "waiting" {
		return false
	}
	g.Player2ID = &playerID
	g.Status = "placing_ships"

	if gs.db != nil {
		gs.db.UpdateGamePlayer2(context.Background(), db.UpdateGamePlayer2Params{
			ID:        g.ID,
			Player2ID: uuid.NullUUID{UUID: playerID, Valid: true},
		})
	}
	return true
}

func (gs *GameStore) CreateBotGame(playerID uuid.UUID) *GameRoom {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	botShips := generateRandomShipSet(rng, BotID)

	g := &GameRoom{
		ID:        uuid.New(),
		Player1ID: playerID,
		Player2ID: &BotID,
		Status:    "placing_ships",
		Ships:     botShips,
		Moves:     []Move{},
		CreatedAt: time.Now(),
	}
	gs.mu.Lock()
	gs.games[g.ID] = g
	gs.mu.Unlock()

	if gs.db != nil {
		gs.db.CreateGame(context.Background(), db.CreateGameParams{
			ID:        g.ID,
			Player1ID: g.Player1ID,
			Status:    g.Status,
			CreatedAt: g.CreatedAt,
		})
		gs.db.UpdateGamePlayer2(context.Background(), db.UpdateGamePlayer2Params{
			ID:        g.ID,
			Player2ID: uuid.NullUUID{UUID: BotID, Valid: true},
		})
		for _, s := range g.Ships {
			gs.db.SaveGameShip(context.Background(), db.SaveGameShipParams{
				ID:         s.ID,
				GameID:     g.ID,
				PlayerID:   s.PlayerID,
				ShipType:   int32(s.ShipType),
				StartX:     int32(s.StartX),
				StartY:     int32(s.StartY),
				Horizontal: s.Horizontal,
			})
		}
	}
	return g
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
	playerShips := 0
	for _, s := range g.Ships {
		if s.PlayerID == playerID {
			playerShips++
		}
	}
	if playerShips > 0 {
		return false
	}

	for i := range ships {
		ships[i].ID = uuid.New()
		ships[i].PlayerID = playerID
		ships[i].Cells = buildCells(ships[i].StartX, ships[i].StartY, ships[i].ShipType, ships[i].Horizontal)
		ships[i].Sunk = false
	}
	g.Ships = append(g.Ships, ships...)

	if gs.db != nil {
		gs.db.DeletePlayerGameShips(context.Background(), db.DeletePlayerGameShipsParams{
			GameID:   g.ID,
			PlayerID: playerID,
		})
		for _, s := range ships {
			gs.db.SaveGameShip(context.Background(), db.SaveGameShipParams{
				ID:         s.ID,
				GameID:     g.ID,
				PlayerID:   s.PlayerID,
				ShipType:   int32(s.ShipType),
				StartX:     int32(s.StartX),
				StartY:     int32(s.StartY),
				Horizontal: s.Horizontal,
			})
		}
	}

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
	defer gs.mu.Unlock()

	g, ok := gs.games[gameID]
	if !ok || g.Status != "placing_ships" || g.Player2ID == nil {
		return
	}
	p1Ships := 0
	p2Ships := 0
	for _, s := range g.Ships {
		if s.PlayerID == g.Player1ID {
			p1Ships++
		} else if g.Player2ID != nil && s.PlayerID == *g.Player2ID {
			p2Ships++
		}
	}
	if p1Ships >= 10 && p2Ships >= 10 {
		g.Status = "playing"
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		if rng.Intn(2) == 0 {
			g.CurrentTurn = &g.Player1ID
		} else {
			g.CurrentTurn = g.Player2ID
		}

		if gs.db != nil {
			var turnUUID uuid.NullUUID
			if g.CurrentTurn != nil {
				turnUUID = uuid.NullUUID{UUID: *g.CurrentTurn, Valid: true}
			}
			gs.db.UpdateGameStatus(context.Background(), db.UpdateGameStatusParams{
				ID:          g.ID,
				Status:      g.Status,
				CurrentTurn: turnUUID,
			})
		}

		if g.CurrentTurn != nil && *g.CurrentTurn == BotID {
			gs.runBotTurn(g)
		}
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
		if m.X == x && m.Y == y {
			return nil, "Эта клетка уже обстреляна"
		}
	}

	var opponentID uuid.UUID
	if playerID == g.Player1ID {
		opponentID = *g.Player2ID
	} else {
		opponentID = g.Player1ID
	}

	hit := gs.recordShot(g, playerID, opponentID, x, y)

	if g.Status == "finished" && gs.db != nil {
		gs.persistGameFinished(g)
		return g, ""
	}

	if !hit {
		if playerID == g.Player1ID {
			g.CurrentTurn = g.Player2ID
		} else {
			g.CurrentTurn = &g.Player1ID
		}
	}

	if gs.db != nil {
		var turnUUID uuid.NullUUID
		if g.CurrentTurn != nil {
			turnUUID = uuid.NullUUID{UUID: *g.CurrentTurn, Valid: true}
		}
		var winnerUUID uuid.NullUUID
		if g.WinnerID != nil {
			winnerUUID = uuid.NullUUID{UUID: *g.WinnerID, Valid: true}
		}
		var finishedAt *time.Time
		if g.Status == "finished" {
			t := time.Now()
			finishedAt = &t
		}
		gs.db.UpdateGameStatus(context.Background(), db.UpdateGameStatusParams{
			ID:          g.ID,
			Status:      g.Status,
			CurrentTurn: turnUUID,
			WinnerID:    winnerUUID,
			FinishedAt:  finishedAt,
		})
	}

	if g.Status == "playing" && g.CurrentTurn != nil && *g.CurrentTurn == BotID {
		gs.runBotTurn(g)
		if g.Status == "finished" && gs.db != nil {
			gs.persistGameFinished(g)
		}
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

func (gs *GameStore) recordShot(g *GameRoom, shooterID, opponentID uuid.UUID, x, y int) bool {
	hit := false
	var sunkShipID *uuid.UUID

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
		PlayerID:   shooterID,
		X:          x,
		Y:          y,
		Hit:        hit,
		SunkShipID: sunkShipID,
	}
	g.Moves = append(g.Moves, move)

	if gs.db != nil {
		var sunkID uuid.NullUUID
		if sunkShipID != nil {
			sunkID = uuid.NullUUID{UUID: *sunkShipID, Valid: true}
		}
		gs.db.SaveGameMove(context.Background(), db.SaveGameMoveParams{
			ID:         move.ID,
			GameID:     g.ID,
			PlayerID:   move.PlayerID,
			X:          int32(move.X),
			Y:          int32(move.Y),
			Hit:        move.Hit,
			SunkShipID: sunkID,
		})
	}

	if hit {
		allSunk := true
		for _, s := range g.Ships {
			if s.PlayerID == opponentID && !s.Sunk {
				allSunk = false
				break
			}
		}
		if allSunk {
			g.Status = "finished"
			g.WinnerID = &shooterID
		}
	}

	return hit
}

func (gs *GameStore) persistGameFinished(g *GameRoom) {
	var turnUUID uuid.NullUUID
	if g.CurrentTurn != nil {
		turnUUID = uuid.NullUUID{UUID: *g.CurrentTurn, Valid: true}
	}
	var winnerUUID uuid.NullUUID
	if g.WinnerID != nil {
		winnerUUID = uuid.NullUUID{UUID: *g.WinnerID, Valid: true}
	}
	t := time.Now()
	gs.db.UpdateGameStatus(context.Background(), db.UpdateGameStatusParams{
		ID:          g.ID,
		Status:      g.Status,
		CurrentTurn: turnUUID,
		WinnerID:    winnerUUID,
		FinishedAt:  &t,
	})
}

func (gs *GameStore) randomUntargetedCell(g *GameRoom, targetPlayerID uuid.UUID) (int, int, bool) {
	var untargeted [][2]int
	moveSet := make(map[[2]int]bool)
	for _, m := range g.Moves {
		moveSet[[2]int{m.X, m.Y}] = true
	}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if !moveSet[[2]int{x, y}] {
				untargeted = append(untargeted, [2]int{x, y})
			}
		}
	}
	if len(untargeted) == 0 {
		return 0, 0, false
	}
	cell := untargeted[rand.Intn(len(untargeted))]
	return cell[0], cell[1], true
}

func (gs *GameStore) runBotTurn(g *GameRoom) {
	for {
		var botID, humanID uuid.UUID
		if g.Player1ID == BotID {
			botID = BotID
			humanID = *g.Player2ID
		} else {
			botID = *g.Player2ID
			humanID = g.Player1ID
		}

		x, y, ok := gs.randomUntargetedCell(g, humanID)
		if !ok {
			break
		}

		hit := gs.recordShot(g, botID, humanID, x, y)

		if g.Status == "finished" {
			if gs.db != nil {
				gs.persistGameFinished(g)
			}
			break
		}

		if !hit {
			g.CurrentTurn = &humanID
			break
		}
	}
}

// LoadActiveGames восстанавливает активные игры из БД в in-memory GameStore.
// Вызывается при старте сервера.
func (gs *GameStore) LoadActiveGames() {
	if gs.db == nil {
		return
	}

	rows, err := gs.db.GetActiveGames(context.Background())
	if err != nil {
		return
	}

	for _, r := range rows {
		player2ID := uuidOrNil(r.Player2ID)
		currentTurn := uuidOrNil(r.CurrentTurn)
		winnerID := uuidOrNil(r.WinnerID)

		shipRows, err := gs.db.GetGameShips(context.Background(), r.ID)
		if err != nil {
			continue
		}

		ships := make([]Ship, 0, len(shipRows))
		for _, sr := range shipRows {
			s := Ship{
				ID:         sr.ID,
				PlayerID:   sr.PlayerID,
				ShipType:   int(sr.ShipType),
				StartX:     int(sr.StartX),
				StartY:     int(sr.StartY),
				Horizontal: sr.Horizontal,
			}
			s.Cells = buildCells(s.StartX, s.StartY, s.ShipType, s.Horizontal)
			ships = append(ships, s)
		}

		moveRows, err := gs.db.GetGameMoves(context.Background(), r.ID)
		if err != nil {
			continue
		}

		moves := make([]Move, 0, len(moveRows))
		for _, mr := range moveRows {
			m := Move{
				ID:       mr.ID,
				PlayerID: mr.PlayerID,
				X:        int(mr.X),
				Y:        int(mr.Y),
				Hit:      mr.Hit,
			}
			if mr.SunkShipID.Valid {
				m.SunkShipID = &mr.SunkShipID.UUID
			}
			moves = append(moves, m)
		}

		// Apply hits to ship cells from moves
		for mi := range moves {
			m := &moves[mi]
			for si := range ships {
				s := &ships[si]
				if s.PlayerID != m.PlayerID {
					// This move targets the opponent's ships
					for ci := range s.Cells {
						if s.Cells[ci].X == m.X && s.Cells[ci].Y == m.Y {
							s.Cells[ci].Hit = true
						}
					}
				}
			}
		}

		// Mark sunken ships
		for si := range ships {
			allHit := true
			for _, c := range ships[si].Cells {
				if !c.Hit {
					allHit = false
					break
				}
			}
			if allHit {
				ships[si].Sunk = true
			}
		}

		g := &GameRoom{
			ID:          r.ID,
			Player1ID:   r.Player1ID,
			Player2ID:   player2ID,
			Status:      r.Status,
			CurrentTurn: currentTurn,
			WinnerID:    winnerID,
			Ships:       ships,
			Moves:       moves,
			CreatedAt:   r.CreatedAt,
		}
		gs.mu.Lock()
		gs.games[g.ID] = g
		gs.mu.Unlock()
	}
}

// finishGameAndUpdateStats завершает игру (форс-финиш) и обновляет статистику профилей.
// Возвращает обновлённый game и winnerID. Вызывается из ForfeitGame и MakeMove.
func (gs *GameStore) FinishGame(gameID uuid.UUID, forfeitingPlayerID uuid.UUID) (*GameRoom, uuid.UUID, string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	g, ok := gs.games[gameID]
	if !ok {
		return nil, uuid.Nil, "Игра не найдена"
	}
	if g.Status != "playing" {
		return nil, uuid.Nil, "Игра не в статусе playing"
	}
	if g.Player1ID != forfeitingPlayerID && (g.Player2ID == nil || *g.Player2ID != forfeitingPlayerID) {
		return nil, uuid.Nil, "Вы не участник этой игры"
	}

	var winnerID uuid.UUID
	if forfeitingPlayerID == g.Player1ID {
		winnerID = *g.Player2ID
	} else {
		winnerID = g.Player1ID
	}
	g.Status = "finished"
	g.WinnerID = &winnerID
	g.CurrentTurn = nil

	if gs.db != nil {
		gs.persistGameFinished(g)
	}
	return g, winnerID, ""
}

func uuidOrNil(nu uuid.NullUUID) *uuid.UUID {
	if nu.Valid {
		return &nu.UUID
	}
	return nil
}

func generateRandomShipSet(rng *rand.Rand, playerID uuid.UUID) []Ship {
	var ships []Ship
	sizes := []int{4, 3, 3, 2, 2, 2, 1, 1, 1, 1}

	for _, size := range sizes {
		placed := false
		for attempt := 0; attempt < 200; attempt++ {
			horizontal := rng.Intn(2) == 0
			startX := rng.Intn(10)
			startY := rng.Intn(10)
			if horizontal && startX+size > 10 {
				continue
			}
			if !horizontal && startY+size > 10 {
				continue
			}

			cells := buildCells(startX, startY, size, horizontal)
			conflict := false
			for _, existing := range ships {
				for _, c := range cells {
					for _, ec := range existing.Cells {
						if absInt(c.X-ec.X) <= 1 && absInt(c.Y-ec.Y) <= 1 {
							conflict = true
							break
						}
					}
					if conflict {
						break
					}
				}
				if conflict {
					break
				}
			}

			if !conflict {
				ships = append(ships, Ship{
					ID:         uuid.New(),
					PlayerID:   playerID,
					ShipType:   size,
					StartX:     startX,
					StartY:     startY,
					Horizontal: horizontal,
					Cells:      cells,
					Sunk:       false,
				})
				placed = true
				break
			}
		}

		if !placed {
			for y := 0; y < 10 && !placed; y++ {
				for x := 0; x < 10 && !placed; x++ {
					horizontal := rng.Intn(2) == 0
					if horizontal && x+size > 10 {
						continue
					}
					if !horizontal && y+size > 10 {
						continue
					}
					cells := buildCells(x, y, size, horizontal)
					conflict := false
					for _, existing := range ships {
						for _, c := range cells {
							for _, ec := range existing.Cells {
								if absInt(c.X-ec.X) <= 1 && absInt(c.Y-ec.Y) <= 1 {
									conflict = true
									break
								}
							}
							if conflict {
								break
							}
						}
						if conflict {
							break
						}
					}
					if !conflict {
						ships = append(ships, Ship{
							ID:         uuid.New(),
							PlayerID:   playerID,
							ShipType:   size,
							StartX:     x,
							StartY:     y,
							Horizontal: horizontal,
							Cells:      cells,
							Sunk:       false,
						})
						placed = true
					}
				}
			}
		}
	}
	return ships
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
