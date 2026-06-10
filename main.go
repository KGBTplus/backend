package main

import (
	"database/sql"
	"log"
	"net/http"

	"github.com/KGBTplus/backend/internal/api"
	"github.com/KGBTplus/backend/internal/db"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

func runMigrations(db *sql.DB) {
	// Укажи путь к папке с твоими .sql миграциями
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatal(err)
	}

	if err := goose.Up(db, "sql/migrations"); err != nil {
		log.Fatalf("Ошибка при накатывании миграций: %v", err)
	}
	log.Println("Миграции успешно применены!")
}

func main() {
	// 1. Подключение к БД
	connStr := "postgres://user:password@localhost:5432/game?sslmode=disable"
	dbConn, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Ошибка подключения к БД: %v", err)
	}
	defer dbConn.Close()

	queries := db.New(dbConn)

	runMigrations(dbConn)

	// 2. Инициализация сервера
	srv := &api.Server{
		DB: queries,
	}

	// 3. Настройка роутера
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("http://localhost:8080/swagger/doc.json"), // Путь к твоему spec файлу
	))

	r.Get("/swagger/doc.json", func(w http.ResponseWriter, r *http.Request) {
		spec, err := api.GetSpecJSON()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(spec)
	})

	// 4. Связка с OpenAPI
	// Поскольку strict-server теперь false, мы передаем srv напрямую
	api.HandlerFromMux(srv, r)

	// (Опционально) Вывод маршрутов для отладки
	log.Println("Зарегистрированные маршруты:")
	chi.Walk(r, func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		log.Printf("%s %s", method, route)
		return nil
	})

	// 5. Запуск
	port := ":8080"
	log.Printf("Сервер запущен на http://localhost%s", port)
	if err := http.ListenAndServe(port, r); err != nil {
		log.Fatalf("Сервер упал: %v", err)
	}
}
