package api

import (
	"encoding/json"
	"log"
	"net/http"
)

// ---------- Матчмейкинг ----------

func (s *Server) JoinMatchmaking(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	if active := s.Games.FindActiveGame(userID); active != nil {
		sendError(w, http.StatusConflict, "Вы уже в активной игре")
		return
	}

	if DebugMode {
		log.Printf("[MM] user=%s joining matchmaking queue", userID)
	}

	// add self to queue
	if err := s.DB.JoinMatchmaking(r.Context(), userID); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка")
		return
	}

	if DebugMode {
		log.Printf("[MM] user=%s added to queue, searching for opponent", userID)
	}

	// try to find an opponent (atomic pop)
	opponent, err := s.DB.PopMatchmakingPair(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка")
		return
	}
	if opponent == nil {
		// no opponent yet – keep waiting
		if DebugMode {
			log.Printf("[MM] user=%s no opponent found, waiting in queue", userID)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "searching"})
		return
	}

	if DebugMode {
		log.Printf("[MM] user=%s matched with opponent=%s", userID, *opponent)
	}

	game := s.CreateGameSession(userID, *opponent)

	if DebugMode {
		log.Printf("[MM] game created: %s between %s and %s", game.ID, userID, *opponent)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"game_id": game.ID.String(),
		"status":  "match_found",
	})
}

func (s *Server) GetMatchmakingStatus(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	inQueue, err := s.DB.GetMatchmakingStatus(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка")
		return
	}

	status := "not_found"
	if inQueue {
		status = "searching"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func (s *Server) LeaveMatchmaking(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	s.DB.LeaveMatchmaking(r.Context(), userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "left_queue"})
}
