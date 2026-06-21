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

	// Add already-connected clients to the room; others will be added
	// when their WS connects via the readPump reconnection logic.
	room := s.Hub.GetOrCreateRoom(game.ID)
	gameIDStr := game.ID.String()
	for _, uid := range []uuid.UUID{creatorID, joinerID} {
		if client, ok := s.Hub.GetClient(uid); ok {
			room.AddClient(client)
			client.SendJSON(WSMessage{
				Type: "match_found",
				Data: mustJSON(MatchFoundData{GameID: gameIDStr}),
			})
		}
	}

	s.DB.DeleteUserLobbies(context.Background(), creatorID)
	s.DB.DeleteUserLobbies(context.Background(), joinerID)

	return game
}
