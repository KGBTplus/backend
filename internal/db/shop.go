package db

import (
	"context"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const updateCoinsAtomic = `UPDATE profiles SET coins = GREATEST(0, coins + $2) WHERE user_id = $1 RETURNING coins`

func (q *Queries) UpdateCoinsAtomic(ctx context.Context, userID uuid.UUID, delta int32) (int32, error) {
	var newCoins int32
	err := q.db.QueryRowContext(ctx, updateCoinsAtomic, userID, delta).Scan(&newCoins)
	return newCoins, err
}

const buyFishAtomic = `UPDATE profiles SET coins = GREATEST(0, coins - $2), total_spent = total_spent + $2 WHERE user_id = $1 AND coins >= $2 RETURNING coins`

func (q *Queries) BuyFishAtomic(ctx context.Context, userID uuid.UUID, price int32) (int32, error) {
	var newCoins int32
	err := q.db.QueryRowContext(ctx, buyFishAtomic, userID, price).Scan(&newCoins)
	return newCoins, err
}

const addGameReward = `UPDATE profiles SET coins = GREATEST(0, coins + $2), total_earned = GREATEST(0, total_earned + $3) WHERE user_id = $1 RETURNING coins`

func (q *Queries) AddGameReward(ctx context.Context, userID uuid.UUID, delta int32, earnedDelta int32) (int32, error) {
	var newCoins int32
	err := q.db.QueryRowContext(ctx, addGameReward, userID, delta, earnedDelta).Scan(&newCoins)
	return newCoins, err
}

const addToInventory = `UPDATE profiles SET inventory = array_append(inventory, $2) WHERE user_id = $1 AND NOT ($2 = ANY(inventory))`

func (q *Queries) AddToInventory(ctx context.Context, userID uuid.UUID, fishID string) error {
	_, err := q.db.ExecContext(ctx, addToInventory, userID, fishID)
	return err
}

const setActiveFish = `UPDATE profiles SET active_fish = $2 WHERE user_id = $1`

func (q *Queries) SetActiveFish(ctx context.Context, userID uuid.UUID, activeFish []string) error {
	_, err := q.db.ExecContext(ctx, setActiveFish, pq.Array(activeFish), userID)
	return err
}

const getProfileShop = `SELECT user_id, total_games, wins, losses, ships_sunk, total_shots, hits, coins, inventory, active_fish, total_spent, total_earned FROM profiles WHERE user_id = $1`

func (q *Queries) GetProfileShop(ctx context.Context, userID uuid.UUID) (Profile, error) {
	row := q.db.QueryRowContext(ctx, getProfileShop, userID)
	var i Profile
	err := row.Scan(
		&i.UserID,
		&i.TotalGames,
		&i.Wins,
		&i.Losses,
		&i.ShipsSunk,
		&i.TotalShots,
		&i.Hits,
		&i.Coins,
		pq.Array(&i.Inventory),
		pq.Array(&i.ActiveFish),
		&i.TotalSpent,
		&i.TotalEarned,
	)
	return i, err
}

const setCoins = `UPDATE profiles SET coins = $2 WHERE user_id = $1`

func (q *Queries) SetCoins(ctx context.Context, userID uuid.UUID, coins int32) error {
	_, err := q.db.ExecContext(ctx, setCoins, userID, coins)
	return err
}
