package api

import (
	"context"
	"database/sql"
)

type FishItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int    `json:"price"`
}

func LoadFishCatalog(ctx context.Context, db *sql.DB) ([]FishItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, description, price FROM fish_shop ORDER BY price ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var catalog []FishItem
	for rows.Next() {
		var f FishItem
		if err := rows.Scan(&f.ID, &f.Name, &f.Description, &f.Price); err != nil {
			return nil, err
		}
		catalog = append(catalog, f)
	}
	return catalog, rows.Err()
}

func GetFishByID(ctx context.Context, db *sql.DB, id string) (*FishItem, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, name, description, price FROM fish_shop WHERE id = $1`, id)
	var f FishItem
	if err := row.Scan(&f.ID, &f.Name, &f.Description, &f.Price); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}
