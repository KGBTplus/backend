package api

import (
	"encoding/json"
	"net/http"
)

// ---------- Лидерборд ----------

func (s *Server) GetLeaderboard(w http.ResponseWriter, r *http.Request, params GetLeaderboardParams) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	limit := int32(50)
	if params.Limit != nil && *params.Limit > 0 {
		limit = int32(*params.Limit)
	}

	rows, err := s.DB.GetLeaderboard(r.Context(), limit)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения таблицы лидеров")
		return
	}

	var top []map[string]interface{}
	for _, r := range rows {
		top = append(top, map[string]interface{}{
			"rank":          r.Rank,
			"player_id":     r.PlayerID,
			"username":      r.Username,
			"wins":          r.Wins,
			"losses":        r.Losses,
			"total_games":   r.TotalGames,
			"total_earned":  r.TotalEarned,
			"total_spent":   r.TotalSpent,
			"win_rate":      r.WinRate,
			"hit_rate":      r.HitRate,
		})
	}

	var myRank interface{}
	myRankRow, err := s.DB.GetPlayerRank(r.Context(), userID)
	if err == nil {
		winRate := 0.0
		if myRankRow.TotalGames > 0 {
			winRate = float64(myRankRow.Wins) / float64(myRankRow.TotalGames) * 100
		}
		hitRate := 0.0
		if myRankRow.TotalShots > 0 {
			hitRate = float64(myRankRow.Hits) / float64(myRankRow.TotalShots) * 100
		}
		myRank = map[string]interface{}{
			"player_id":     myRankRow.ID,
			"username":      myRankRow.Username,
			"wins":          myRankRow.Wins,
			"losses":        myRankRow.Losses,
			"total_games":   myRankRow.TotalGames,
			"total_earned":  myRankRow.TotalEarned,
			"total_spent":   myRankRow.TotalSpent,
			"win_rate":      winRate,
			"hit_rate":      hitRate,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"top":     top,
		"my_rank": myRank,
	})
}
