package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/KGBTplus/backend/internal/db"
	"github.com/google/uuid"
)

type AchievementStatus struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	RewardCoins    int32  `json:"reward_coins"`
	ConditionType  string `json:"condition_type"`
	ConditionValue int32  `json:"condition_value"`
	Completed      bool   `json:"completed"`
	RewardClaimed  bool   `json:"reward_claimed"`
	Progress       int32  `json:"progress"`
}

type AchievementUnlockedData struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RewardCoins int32  `json:"reward_coins"`
}

type ClaimRequest struct {
	AchievementID string `json:"achievement_id"`
}

type ClaimResponse struct {
	NewBalance       int32  `json:"new_balance"`
	AchievementID    string `json:"achievement_id"`
}

func (s *Server) GetAchievementsList(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		http.Error(w, "Не авторизован", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()

	allAchievements, err := s.DB.GetAllAchievements(ctx)
	if err != nil {
		log.Printf("[ACHIEVEMENTS] GetAllAchievements error: %v", err)
		http.Error(w, "Ошибка загрузки достижений", http.StatusInternalServerError)
		return
	}

	userAchievements, err := s.DB.GetUserAchievements(ctx, userID)
	if err != nil {
		log.Printf("[ACHIEVEMENTS] GetUserAchievements error: %v", err)
		http.Error(w, "Ошибка загрузки прогресса", http.StatusInternalServerError)
		return
	}

	uaMap := make(map[uuid.UUID]db.UserAchievement)
	for _, ua := range userAchievements {
		uaMap[ua.AchievementID] = ua
	}

	result := make([]AchievementStatus, 0, len(allAchievements))
	for _, a := range allAchievements {
		ua, exists := uaMap[a.ID]
		status := AchievementStatus{
			ID:             a.ID.String(),
			Name:           a.Name,
			Description:    a.Description,
			RewardCoins:    a.RewardCoins,
			ConditionType:  a.ConditionType,
			ConditionValue: a.ConditionValue,
			Completed:      exists && ua.CompletedAt != nil,
			RewardClaimed:  exists && ua.RewardClaimedAt != nil,
			Progress:       ua.Progress,
		}
		result = append(result, status)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) ClaimAchievement(w http.ResponseWriter, r *http.Request) {
	userID, err := s.getUserIDFromToken(r)
	if err != nil {
		http.Error(w, "Не авторизован", http.StatusUnauthorized)
		return
	}

	var req ClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Неверный запрос", http.StatusBadRequest)
		return
	}

	achID, err := uuid.Parse(req.AchievementID)
	if err != nil {
		http.Error(w, "Неверный ID достижения", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	ach, err := s.DB.GetAchievementByID(ctx, achID)
	if err != nil {
		http.Error(w, "Достижение не найдено", http.StatusNotFound)
		return
	}

	tx, err := s.SQLDB.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("[ACHIEVEMENTS] BeginTx error: %v", err)
		http.Error(w, "Ошибка сервера", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	newCoins, err := s.DB.ClaimRewardAtomic(ctx, tx, userID, achID)
	if err != nil {
		log.Printf("[ACHIEVEMENTS] ClaimRewardAtomic error: %v", err)
		http.Error(w, "Награда уже получена или недоступна", http.StatusBadRequest)
		return
	}

	if newCoins == 0 {
		log.Printf("[ACHIEVEMENTS] claim failed for user=%s achievement=%s: no rows", userID, achID)
		http.Error(w, "Награда уже получена или недоступна", http.StatusBadRequest)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[ACHIEVEMENTS] Commit error: %v", err)
		http.Error(w, "Ошибка сервера", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ClaimResponse{
		NewBalance:       newCoins,
		AchievementID:    ach.ID.String(),
	})
	_ = ach
}

func (s *Server) CheckAchievements(ctx context.Context, userID uuid.UUID, profile db.Profile) []AchievementUnlockedData {
	allAchievements, err := s.DB.GetAllAchievements(ctx)
	if err != nil {
		log.Printf("[ACHIEVEMENTS] GetAllAchievements error: %v", err)
		return nil
	}

	userAchievements, err := s.DB.GetUserAchievements(ctx, userID)
	if err != nil {
		log.Printf("[ACHIEVEMENTS] GetUserAchievements error: %v", err)
		return nil
	}

	completedMap := make(map[uuid.UUID]bool)
	progressMap := make(map[uuid.UUID]int32)
	for _, ua := range userAchievements {
		completedMap[ua.AchievementID] = ua.CompletedAt != nil
		progressMap[ua.AchievementID] = ua.Progress
	}

	var unlocked []AchievementUnlockedData

	for _, a := range allAchievements {
		if completedMap[a.ID] {
			continue
		}

		var currentValue int32
		switch a.ConditionType {
		case "matches_played":
			currentValue = profile.TotalGames
		case "wins":
			currentValue = profile.Wins
		case "ships_killed":
			currentValue = profile.ShipsSunk
		default:
			continue
		}

		if err := s.DB.UpsertUserAchievementProgress(ctx, userID, a.ID, currentValue); err != nil {
			log.Printf("[ACHIEVEMENTS] UpsertProgress error: %v", err)
			continue
		}

		if currentValue >= a.ConditionValue {
			if err := s.DB.CompleteUserAchievement(ctx, userID, a.ID); err != nil {
				log.Printf("[ACHIEVEMENTS] CompleteAchievement error: %v", err)
				continue
			}
			unlocked = append(unlocked, AchievementUnlockedData{
				ID:          a.ID.String(),
				Name:        a.Name,
				Description: a.Description,
				RewardCoins: a.RewardCoins,
			})
		}
	}

	return unlocked
}

func (s *Server) sendAchievementUnlocked(userID uuid.UUID, data AchievementUnlockedData) {
	s.Hub.SendToClient(userID, WSMessage{
		Type: "achievement_unlocked",
		Data: mustJSON(data),
	})
}
