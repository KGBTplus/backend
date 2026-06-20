package api

import (
	"context"
	"log"

	"github.com/google/uuid"
)

func (s *Server) CreateGameSession(creatorID, joinerID uuid.UUID) *GameRoom {
	game := s.Games.Create(creatorID)
	s.Games.Join(game.ID, joinerID)

	game.IsRevanchReady1 = false
	game.IsRevanchReady2 = false

	s.startPlacementTimer(game.ID)

	log.Printf("[SESSION] game created: %s, p1=%s p2=%s", game.ID, creatorID, joinerID)

	room := s.Hub.GetOrCreateRoom(game.ID)
	if client, ok := s.Hub.GetClient(creatorID); ok {
		room.AddClient(client)
	}
	if client, ok := s.Hub.GetClient(joinerID); ok {
		room.AddClient(client)
	}

	gameIDStr := game.ID.String()
	s.Hub.SendToClient(creatorID, WSMessage{
		Type: "match_found",
		Data: mustJSON(MatchFoundData{GameID: gameIDStr}),
	})
	s.Hub.SendToClient(joinerID, WSMessage{
		Type: "match_found",
		Data: mustJSON(MatchFoundData{GameID: gameIDStr}),
	})

	s.DB.DeleteUserLobbies(context.Background(), creatorID)
	s.DB.DeleteUserLobbies(context.Background(), joinerID)

	return game
}
