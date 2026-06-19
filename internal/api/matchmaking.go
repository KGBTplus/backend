package api

import (
	"encoding/json"
	"net/http"
)

// ---------- Матчмейкинг ----------

func (s *Server) JoinMatchmaking(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	if err := s.DB.JoinMatchmaking(r.Context(), userID); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "searching"})
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
