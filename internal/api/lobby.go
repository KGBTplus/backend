package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (s *Server) lobbyToMap(ctx context.Context, l db.Lobby, players []uuid.UUID) map[string]interface{} {
	usernames := make([]string, 0, len(players))
	for _, pid := range players {
		u, err := s.DB.GetUserByID(ctx, pid)
		if err == nil {
			usernames = append(usernames, u.Username)
		} else {
			usernames = append(usernames, pid.String()[:8])
		}
	}
	m := map[string]interface{}{
		"id":          l.ID,
		"creator_id":  l.CreatorID,
		"creator_name": func() string {
			cu, err := s.DB.GetUserByID(ctx, l.CreatorID)
			if err != nil {
				return l.CreatorID.String()[:8]
			}
			return cu.Username
		}(),
		"status":      l.Status,
		"players":     players,
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
		result = append(result, s.lobbyToMap(r.Context(), l, players))
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
	json.NewEncoder(w).Encode(s.lobbyToMap(r.Context(), l, []uuid.UUID{userID}))
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

	result := s.lobbyToMap(r.Context(), l, players)
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
		game := s.Games.Create(l.CreatorID)
		log.Printf("[LOBBY] game created: %s, creator=%s, status=%s", game.ID, l.CreatorID, game.Status)
		joined := s.Games.Join(game.ID, userID)
		log.Printf("[LOBBY] join result: %v, game status after=%s, p2=%v", joined, game.Status, game.Player2ID)
		s.DB.DeleteLobby(r.Context(), l.ID)

		// Start the 90-second placement timer
		s.startPlacementTimer(game.ID)

		// create room and add creator's WS client if connected
		room := s.Hub.GetOrCreateRoom(game.ID)
		log.Printf("[LOBBY] room %s has %d clients before adding", game.ID, len(room.Clients))
		if client, ok := s.Hub.GetClient(l.CreatorID); ok {
			room.AddClient(client)
			log.Printf("[LOBBY] added creator %s to room", l.CreatorID)
		} else {
			log.Printf("[LOBBY] creator %s NOT connected to WS", l.CreatorID)
		}

		// notify creator via WS
		s.Hub.SendToClient(l.CreatorID, WSMessage{
			Type: "match_found",
			Data: mustJSON(MatchFoundData{GameID: game.ID.String()}),
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"game_id": game.ID.String(),
			"status":  "game_ready",
		})
		return
	}

	if players == nil {
		players = []uuid.UUID{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.lobbyToMap(r.Context(), l, players))
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
	json.NewEncoder(w).Encode(s.lobbyToMap(r.Context(), l, players))
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
	json.NewEncoder(w).Encode(s.lobbyToMap(r.Context(), l, players))
}
