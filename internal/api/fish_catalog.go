package api

type FishItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

var FishCatalog = []FishItem{
	{ID: "clownfish",  Name: "Рыба-клоун",    Price: 100},
	{ID: "goldfish",   Name: "Золотая рыбка", Price: 250},
	{ID: "angelfish",  Name: "Рыба-ангел",    Price: 500},
	{ID: "neon_tetra", Name: "Неоновая тетра", Price: 150},
	{ID: "betta",      Name: "Петушок",       Price: 750},
	{ID: "shark",      Name: "Акула",         Price: 1000},
}

func FishByID(id string) *FishItem {
	for _, f := range FishCatalog {
		if f.ID == id {
			return &f
		}
	}
	return nil
}
