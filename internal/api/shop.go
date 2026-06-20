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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FishCatalog)
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

	fish := FishByID(req.FishID)
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

	_, err = s.DB.BuyFishAtomic(r.Context(), userID, int32(fish.Price))
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка списания монет")
		return
	}

	if err := s.DB.AddToInventory(r.Context(), userID, req.FishID); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка добавления в инвентарь")
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

func (s *Server) EquipFish(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		sendError(w, http.StatusUnauthorized, "Не авторизован")
		return
	}

	var req struct {
		FishIDs []string `json:"fishIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "Неверный формат JSON")
		return
	}

	if len(req.FishIDs) == 0 {
		sendError(w, http.StatusBadRequest, "Выберите хотя бы одну рыбку")
		return
	}

	profile, err := s.DB.GetProfileShop(r.Context(), userID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка получения профиля")
		return
	}

	invSet := make(map[string]bool, len(profile.Inventory))
	for _, f := range profile.Inventory {
		invSet[f] = true
	}

	for _, fishID := range req.FishIDs {
		if !invSet[fishID] {
			sendError(w, http.StatusBadRequest, "Рыбка "+fishID+" не найдена в инвентаре")
			return
		}
	}

	if err := s.DB.SetActiveFish(r.Context(), userID, req.FishIDs); err != nil {
		sendError(w, http.StatusInternalServerError, "Ошибка сохранения")
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


