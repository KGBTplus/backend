package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (s *Server) lobbyToMap(ctx context.Context, l *MemoryLobby) map[string]interface{} {
	usernames := make([]string, 0, len(l.Players))
	for _, pid := range l.Players {
		u, err := s.DB.GetUserByID(ctx, pid)
		if err == nil {
			usernames = append(usernames, u.Username)
		} else {
			usernames = append(usernames, pid.String()[:8])
		}
	}
	m := map[string]interface{}{
		"id":           l.ID,
		"creator_id":   l.CreatorID,
		"creator_name": func() string {
			cu, err := s.DB.GetUserByID(ctx, l.CreatorID)
			if err != nil {
				return l.CreatorID.String()[:8]
			}
			return cu.Username
		}(),
		"status":      l.Status,
		"players":     l.Players,
		"usernames":   usernames,
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

	rows := s.Lobbies.List(status)
	var result []map[string]interface{}
	for _, l := range rows {
		result = append(result, s.lobbyToMap(r.Context(), l))
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

	l := s.Lobbies.Create(userID)
	s.broadcastLobbyList()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(s.lobbyToMap(r.Context(), l))
}

func (s *Server) GetLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	l, ok := s.Lobbies.Get(uuid.UUID(lobbyID))
	if !ok {
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}

	result := s.lobbyToMap(r.Context(), l)
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

	l, err2 := s.Lobbies.Join(uuid.UUID(lobbyID), userID)
	if err2 != nil {
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}

	if l.Status == "full" {
		s.broadcastLobbyList()

		game := s.CreateGameSession(l.CreatorID, userID)
		s.Lobbies.Delete(l.ID)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"game_id": game.ID.String(),
			"status":  "game_ready",
		})
		return
	}

	s.broadcastLobbyList()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.lobbyToMap(r.Context(), l))
}

func (s *Server) LeaveLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	_, deleted, err2 := s.Lobbies.Leave(uuid.UUID(lobbyID), userID)
	if err2 != nil {
		sendError(w, http.StatusBadRequest, "Вы не в этом лобби")
		return
	}

	s.broadcastLobbyList()

	if deleted {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "lobby deleted"})
		return
	}

	l, ok := s.Lobbies.Get(uuid.UUID(lobbyID))
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "lobby deleted"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.lobbyToMap(r.Context(), l))
}

func (s *Server) DeleteLobby(w http.ResponseWriter, r *http.Request, lobbyID openapi_types.UUID) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	l, ok := s.Lobbies.Get(uuid.UUID(lobbyID))
	if !ok {
		sendError(w, http.StatusNotFound, "Лобби не найдено")
		return
	}
	if l.CreatorID != userID {
		sendError(w, http.StatusForbidden, "Только создатель может удалить лобби")
		return
	}

	s.Lobbies.Delete(l.ID)
	s.broadcastLobbyList()

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

	l, ok := s.Lobbies.FindByCode(req.Code)
	if !ok {
		sendError(w, http.StatusNotFound, "Лобби с таким кодом не найдено")
		return
	}
	if l.Status != "waiting" {
		sendError(w, http.StatusConflict, "Лобби уже заполнено")
		return
	}

	l, err2 := s.Lobbies.Join(l.ID, userID)
	if err2 != nil {
		sendError(w, http.StatusConflict, "Вы уже в лобби")
		return
	}

	s.broadcastLobbyList()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.lobbyToMap(r.Context(), l))
}

func (s *Server) broadcastLobbyList() {
	rows := s.Lobbies.List("")
	var result []map[string]interface{}
	for _, l := range rows {
		result = append(result, s.lobbyToMap(context.Background(), l))
	}
	if result == nil {
		result = []map[string]interface{}{}
	}

	msg := WSMessage{
		Type: "lobby_list",
		Data: mustJSON(result),
	}
	s.Hub.mu.RLock()
	for _, client := range s.Hub.clients {
		client.SendJSON(msg)
	}
	s.Hub.mu.RUnlock()
}
