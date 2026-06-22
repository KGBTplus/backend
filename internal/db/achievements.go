package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Achievement struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	RewardCoins    int32     `json:"reward_coins"`
	ConditionType  string    `json:"condition_type"`
	ConditionValue int32     `json:"condition_value"`
	CreatedAt      time.Time `json:"created_at"`
}

type UserAchievement struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"user_id"`
	AchievementID   uuid.UUID  `json:"achievement_id"`
	CompletedAt     *time.Time `json:"completed_at"`
	RewardClaimedAt *time.Time `json:"reward_claimed_at"`
	Progress        int32      `json:"progress"`
}

const getAllAchievements = `SELECT id, name, description, reward_coins, condition_type, condition_value, created_at FROM achievements ORDER BY reward_coins ASC`

func (q *Queries) GetAllAchievements(ctx context.Context) ([]Achievement, error) {
	rows, err := q.db.QueryContext(ctx, getAllAchievements)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Achievement
	for rows.Next() {
		var a Achievement
		err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.RewardCoins, &a.ConditionType, &a.ConditionValue, &a.CreatedAt)
		if err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

const getUserAchievements = `SELECT id, user_id, achievement_id, completed_at, reward_claimed_at, progress FROM user_achievements WHERE user_id = $1`

func (q *Queries) GetUserAchievements(ctx context.Context, userID uuid.UUID) ([]UserAchievement, error) {
	rows, err := q.db.QueryContext(ctx, getUserAchievements, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []UserAchievement
	for rows.Next() {
		var ua UserAchievement
		err := rows.Scan(&ua.ID, &ua.UserID, &ua.AchievementID, &ua.CompletedAt, &ua.RewardClaimedAt, &ua.Progress)
		if err != nil {
			return nil, err
		}
		items = append(items, ua)
	}
	return items, rows.Err()
}

const upsertUserAchievementProgress = `
INSERT INTO user_achievements (user_id, achievement_id, progress)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, achievement_id)
DO UPDATE SET progress = GREATEST(user_achievements.progress, $3)
`

func (q *Queries) UpsertUserAchievementProgress(ctx context.Context, userID uuid.UUID, achievementID uuid.UUID, progress int32) error {
	_, err := q.db.ExecContext(ctx, upsertUserAchievementProgress, userID, achievementID, progress)
	return err
}

const completeUserAchievement = `
UPDATE user_achievements
SET completed_at = NOW()
WHERE user_id = $1 AND achievement_id = $2 AND completed_at IS NULL
`

func (q *Queries) CompleteUserAchievement(ctx context.Context, userID uuid.UUID, achievementID uuid.UUID) error {
	_, err := q.db.ExecContext(ctx, completeUserAchievement, userID, achievementID)
	return err
}

const claimAchievementReward = `
UPDATE user_achievements
SET reward_claimed_at = NOW()
WHERE user_id = $1 AND achievement_id = $2
  AND completed_at IS NOT NULL
  AND reward_claimed_at IS NULL
`

func (q *Queries) ClaimAchievementReward(ctx context.Context, userID uuid.UUID, achievementID uuid.UUID) error {
	_, err := q.db.ExecContext(ctx, claimAchievementReward, userID, achievementID)
	return err
}

const getAchievementByID = `SELECT id, name, description, reward_coins, condition_type, condition_value, created_at FROM achievements WHERE id = $1`

func (q *Queries) GetAchievementByID(ctx context.Context, id uuid.UUID) (Achievement, error) {
	var a Achievement
	err := q.db.QueryRowContext(ctx, getAchievementByID, id).Scan(&a.ID, &a.Name, &a.Description, &a.RewardCoins, &a.ConditionType, &a.ConditionValue, &a.CreatedAt)
	return a, err
}

const addCoinsForAchievement = `UPDATE profiles SET coins = coins + $2 WHERE user_id = $1 RETURNING coins`

func (q *Queries) AddCoinsForAchievement(ctx context.Context, userID uuid.UUID, amount int32) (int32, error) {
	var newCoins int32
	err := q.db.QueryRowContext(ctx, addCoinsForAchievement, userID, amount).Scan(&newCoins)
	return newCoins, err
}

const claimRewardAtomic = `
WITH reward AS (
    SELECT ua.id, a.reward_coins
    FROM user_achievements ua
    JOIN achievements a ON a.id = ua.achievement_id
    WHERE ua.user_id = $1 AND ua.achievement_id = $2
      AND ua.completed_at IS NOT NULL
      AND ua.reward_claimed_at IS NULL
    FOR UPDATE OF ua
),
update_ua AS (
    UPDATE user_achievements
    SET reward_claimed_at = NOW()
    WHERE id IN (SELECT id FROM reward)
)
UPDATE profiles p
SET coins = p.coins + (SELECT reward_coins FROM reward)
WHERE p.user_id = $1
  AND EXISTS (SELECT 1 FROM reward)
RETURNING p.coins
`

func (q *Queries) ClaimRewardAtomic(ctx context.Context, tx *sql.Tx, userID uuid.UUID, achievementID uuid.UUID) (int32, error) {
	var newCoins int32
	err := tx.QueryRowContext(ctx, claimRewardAtomic, userID, achievementID).Scan(&newCoins)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return newCoins, err
}
