package api

import (
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/lib/pq"
)

// ---------- Профиль ----------

func (s *Server) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	user, err := s.DB.GetUserByID(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения пользователя")
		return
	}

	profile, err := s.DB.GetProfile(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения профиля")
		return
	}

	var winPct, hitPct float64
	if profile.TotalGames > 0 {
		winPct = float64(profile.Wins) / float64(profile.TotalGames) * 100
	}
	if profile.TotalShots > 0 {
		hitPct = float64(profile.Hits) / float64(profile.TotalShots) * 100
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":               user.ID,
		"username":         user.Username,
		"email_masked":     maskEmail(user.Email),
		"created_at":       user.CreatedAt,
		"otp_enabled":      user.EmailOtpEnabled,
		"total_games":      profile.TotalGames,
		"wins":             profile.Wins,
		"losses":           profile.Losses,
		"win_percentage":   winPct,
		"ships_sunk":       profile.ShipsSunk,
		"total_shots":      profile.TotalShots,
		"hits":             profile.Hits,
		"hit_percentage":   hitPct,
		"coins":            profile.Coins,
		"inventory":        profile.Inventory,
		"active_fish":      profile.ActiveFish,
		"total_spent":      profile.TotalSpent,
		"total_earned":     profile.TotalEarned,
	})
}

func (s *Server) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if utf8.RuneCountInString(req.Username) < minUsernameLen || utf8.RuneCountInString(req.Username) > maxUsernameLen {
		sendError(w, http.StatusBadRequest, "Имя пользователя должно содержать от 4 до 16 символов")
		return
	}
	if !usernameRegex.MatchString(req.Username) {
		sendError(w, http.StatusBadRequest, "Имя пользователя может содержать только латинские буквы, цифры и _")
		return
	}

	if err := s.DB.UpdateUsername(r.Context(), db.UpdateUsernameParams{
		ID:       userID,
		Username: req.Username,
	}); err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			sendError(w, http.StatusConflict, "Никнейм уже занят")
			return
		}
		sendError(w, http.StatusInternalServerError, "Ошибка обновления профиля")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"username": req.Username})
}
