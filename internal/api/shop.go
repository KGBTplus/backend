package api

import (
	"encoding/json"
	"net/http"
)

const (
	WIN_REWARD        = 25
	DRAW_REWARD       = 10
	LOSE_REWARD       = -5
	HIT_REWARD        = 1
	PERFECT_WIN_BONUS = 20
	RANDOM_FACTOR_MIN = -0.05
	RANDOM_FACTOR_MAX = 0.05

	EARLY_MOVE_THRESHOLD = 10
	LATE_MOVE_THRESHOLD  = 25
	EARLY_MULTIPLIER     = 0.5
	NORMAL_MULTIPLIER    = 1.0
	LATE_MULTIPLIER      = 1.5

	FORFEIT_WIN_MIN   = 5
	FORFEIT_WIN_MAX   = 25
	FORFEIT_LOSE_MIN  = 0
	FORFEIT_LOSE_MAX  = -5
	FORFEIT_MAX_MOVES = 30
)

func (s *Server) GetProfileShop(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	profile, err := s.DB.GetProfileShop(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения профиля")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"coins":       profile.Coins,
		"inventory":   profile.Inventory,
		"active_fish": profile.ActiveFish,
	})
}

func (s *Server) GetShop(w http.ResponseWriter, r *http.Request) {
	_, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	catalog, err := LoadFishCatalog(r.Context(), s.SQLDB)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка загрузки каталога")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(catalog)
}

func (s *Server) GetInventory(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	inventory, err := s.DB.GetInventory(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка загрузки инвентаря")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"inventory": inventory,
	})
}

func (s *Server) ToggleFish(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		FishID string `json:"fishId"`
		Active bool   `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if req.FishID == "" {
		sendError(w, http.StatusBadRequest, "fishId обязателен")
		return
	}

	fish, err := GetFishByID(r.Context(), s.SQLDB, req.FishID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка проверки рыбки")
		return
	}
	if fish == nil {
		sendError(w, http.StatusNotFound, "Рыбка не найдена")
		return
	}

	profile, err := s.DB.GetProfileShop(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения профиля")
		return
	}

	owned := false
	for _, f := range profile.Inventory {
		if f == req.FishID {
			owned = true
			break
		}
	}
	if !owned {
		sendError(w, http.StatusBadRequest, "Рыбка не куплена")
		return
	}

	if err := s.DB.ToggleActiveFish(r.Context(), userID, req.FishID, req.Active); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка переключения")
		return
	}

	updatedProfile, err := s.DB.GetProfileShop(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения профиля")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"coins":       updatedProfile.Coins,
		"inventory":   updatedProfile.Inventory,
		"active_fish": updatedProfile.ActiveFish,
	})
}

func (s *Server) BuyFish(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		FishID string `json:"fishId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if req.FishID == "" {
		sendError(w, http.StatusBadRequest, "fishId обязателен")
		return
	}

	fish, err := GetFishByID(r.Context(), s.SQLDB, req.FishID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка проверки рыбки")
		return
	}
	if fish == nil {
		sendError(w, http.StatusNotFound, "Рыбка не найдена")
		return
	}

	profile, err := s.DB.GetProfileShop(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения профиля")
		return
	}

	for _, f := range profile.Inventory {
		if f == req.FishID {
			sendError(w, http.StatusConflict, "Рыбка уже куплена")
			return
		}
	}

	if int(profile.Coins) < fish.Price {
		sendError(w, http.StatusBadRequest, "Недостаточно средств")
		return
	}

	if err := s.DB.BuyFishAtomicTx(r.Context(), s.SQLDB, userID, req.FishID, int32(fish.Price)); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка покупки")
		return
	}

	updatedProfile, err := s.DB.GetProfileShop(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения профиля")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"coins":       updatedProfile.Coins,
		"inventory":   updatedProfile.Inventory,
		"active_fish": updatedProfile.ActiveFish,
	})
}
