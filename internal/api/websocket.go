package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096
)

// ---------- WS message types ----------

type WSMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type MatchFoundData struct {
	GameID string `json:"game_id"`
}

type GameStartedData struct {
	GameID      string `json:"game_id"`
	CurrentTurn string `json:"current_turn"`
}

type OpponentMovedData struct {
	GameID   string `json:"game_id"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Hit      bool   `json:"hit"`
	ShipSunk bool   `json:"ship_sunk"`
}

type YourTurnData struct {
	GameID      string `json:"game_id"`
	MoveDeadline string `json:"move_deadline"`
}

type TimerData struct {
	SecondsLeft int `json:"seconds_left"`
}

type GameOverData struct {
	GameID         string `json:"game_id"`
	WinnerID       string `json:"winner_id"`
	WinnerUsername string `json:"winner_username"`
	WinReason      string `json:"win_reason"`
	Player1Sunk    int    `json:"player1_ships_sunk"`
	Player2Sunk    int    `json:"player2_ships_sunk"`
}

type RematchData struct {
	GameID    string `json:"game_id,omitempty"`
	NewGameID string `json:"new_game_id,omitempty"`
}

type ErrorData struct {
	Message string `json:"message"`
}

// ---------- Client ----------

type Client struct {
	Server *Server
	UserID uuid.UUID
	Conn   *websocket.Conn
	Send   chan []byte
	Room   *Room
	mu     sync.Mutex
}

func (c *Client) SendJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.Send <- data:
	default:
	}
}

// ---------- Room ----------

type Room struct {
	GameID  uuid.UUID
	Clients map[uuid.UUID]*Client
	mu      sync.RWMutex
}

func NewRoom(gameID uuid.UUID) *Room {
	return &Room{
		GameID:  gameID,
		Clients: make(map[uuid.UUID]*Client),
	}
}

func (r *Room) AddClient(c *Client) {
	r.mu.Lock()
	r.Clients[c.UserID] = c
	c.Room = r
	r.mu.Unlock()
}

func (r *Room) RemoveClient(userID uuid.UUID) {
	r.mu.Lock()
	delete(r.Clients, userID)
	r.mu.Unlock()
}

func (r *Room) Broadcast(v interface{}, exclude uuid.UUID) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id, c := range r.Clients {
		if id != exclude {
			select {
			case c.Send <- data:
			default:
			}
		}
	}
}

func (r *Room) GetOtherClient(userID uuid.UUID) *Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id, c := range r.Clients {
		if id != userID {
			return c
		}
	}
	return nil
}

// ---------- Hub ----------

type Hub struct {
	mu         sync.RWMutex
	rooms      map[uuid.UUID]*Room
	clients    map[uuid.UUID]*Client
	matchmaking []uuid.UUID
}

func NewHub() *Hub {
	return &Hub{
		rooms:      make(map[uuid.UUID]*Room),
		clients:    make(map[uuid.UUID]*Client),
		matchmaking: []uuid.UUID{},
	}
}

func (h *Hub) GetOrCreateRoom(gameID uuid.UUID) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[gameID]; ok {
		return r
	}
	r := NewRoom(gameID)
	h.rooms[gameID] = r
	return r
}

func (h *Hub) GetRoom(gameID uuid.UUID) *Room {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.rooms[gameID]
}

func (h *Hub) RegisterClient(c *Client) {
	h.mu.Lock()
	h.clients[c.UserID] = c
	h.mu.Unlock()
}

func (h *Hub) UnregisterClient(userID uuid.UUID) {
	h.mu.Lock()
	client, ok := h.clients[userID]
	if ok && client.Room != nil {
		client.Room.RemoveClient(userID)
	}
	delete(h.clients, userID)
	// remove from matchmaking if present
	for i, uid := range h.matchmaking {
		if uid == userID {
			h.matchmaking = append(h.matchmaking[:i], h.matchmaking[i+1:]...)
			break
		}
	}
	h.mu.Unlock()
}

func (h *Hub) FindMatch(userID uuid.UUID) *uuid.UUID {
	h.mu.Lock()
	defer h.mu.Unlock()
	// don't add duplicates
	for _, uid := range h.matchmaking {
		if uid == userID {
			return nil
		}
	}
	// look for an opponent
	for _, uid := range h.matchmaking {
		client, ok := h.clients[uid]
		if ok && uid != userID && (client.Room == nil) {
			// remove opponent from queue
			for i, u := range h.matchmaking {
				if u == uid {
					h.matchmaking = append(h.matchmaking[:i], h.matchmaking[i+1:]...)
					break
				}
			}
			return &uid
		}
	}
	h.matchmaking = append(h.matchmaking, userID)
	return nil
}

// ---------- WebSocket handler ----------

func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Error(w, "Missing token", http.StatusBadRequest)
		return
	}

	userID, err := s.parseJWT(tokenStr)
	if err != nil {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	client := &Client{
		Server: s,
		UserID: userID,
		Conn:   conn,
		Send:   make(chan []byte, 256),
	}

	s.Hub.RegisterClient(client)

	go client.writePump()
	go client.readPump()
}

func (s *Server) parseJWT(tokenStr string) (uuid.UUID, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return s.JWTKey, nil
	})
	if err != nil || !token.Valid {
		return uuid.Nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return uuid.Nil, jwt.ErrSignatureInvalid
	}
	sub, ok := claims["sub"].(string)
	if !ok {
		return uuid.Nil, jwt.ErrSignatureInvalid
	}
	return uuid.Parse(sub)
}

// ---------- read / write pumps ----------

func (c *Client) readPump() {
	defer func() {
		c.Server.Hub.UnregisterClient(c.UserID)
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// notify of matchmaking status
	c.SendJSON(WSMessage{Type: "matchmaking_searching"})

	// try to find a match
	opponentID := c.Server.Hub.FindMatch(c.UserID)
	if opponentID != nil {
		game := c.Server.Games.Create(c.UserID)
		c.Server.Games.Join(game.ID, *opponentID)

		room := c.Server.Hub.GetOrCreateRoom(game.ID)
		room.AddClient(c)

		opponentClient, ok := func() (*Client, bool) {
			c.Server.Hub.mu.RLock()
			defer c.Server.Hub.mu.RUnlock()
			cl, ok := c.Server.Hub.clients[*opponentID]
			return cl, ok
		}()
		if ok {
			room.AddClient(opponentClient)
			gameIDStr := game.ID.String()

			c.SendJSON(WSMessage{
				Type: "match_found",
				Data: mustJSON(MatchFoundData{GameID: gameIDStr}),
			})
			opponentClient.SendJSON(WSMessage{
				Type: "match_found",
				Data: mustJSON(MatchFoundData{GameID: gameIDStr}),
			})
		}
	}

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("WS error: %v", err)
			}
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "ping":
			c.SendJSON(WSMessage{Type: "pong"})
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ---------- helpers ----------

func mustJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
