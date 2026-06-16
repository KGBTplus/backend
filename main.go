package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"

	"github.com/KGBTplus/backend/internal/api"
	"github.com/KGBTplus/backend/internal/db"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

func runMigrations(db *sql.DB) {
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatal(err)
	}

	if err := goose.Up(db, "sql/migrations"); err != nil {
		log.Fatalf("Ошибка при накатывании миграций: %v", err)
	}
	log.Println("Миграции успешно применены!")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	godotenv.Load()

	// 1. Подключение к БД
	connStr := getEnv("DATABASE_URL", "postgres://user:password@localhost:5432/game?sslmode=disable")
	dbConn, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Ошибка подключения к БД: %v", err)
	}
	defer dbConn.Close()

	queries := db.New(dbConn)

	runMigrations(dbConn)

	// 2. SMTP конфигурация (можно не заполнять — код будет печататься в лог)
	smtpCfg := api.SMTPConfig{
		Host:     getEnv("SMTP_HOST", ""),
		Username: getEnv("SMTP_USERNAME", ""),
		Password: getEnv("SMTP_PASSWORD", ""),
		From:     getEnv("SMTP_FROM", "noreply@seabattle.ru"),
	}

	// 3. Инициализация сервера
	jwtSecret := getEnv("JWT_SECRET", "my_secret_key")
	srv := api.NewServer(queries, smtpCfg, jwtSecret)

	// 4. Настройка роутера
	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://team4.verstack.ru", "http://team4.verstack.ru", "http://localhost:3000"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
	}))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
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

	// 5. Связка с OpenAPI
	api.HandlerFromMux(srv, r)

	// (Опционально) Вывод маршрутов для отладки
	log.Println("Зарегистрированные маршруты:")
	chi.Walk(r, func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		log.Printf("%s %s", method, route)
		return nil
	})

	// 6. Запуск
	port := ":8080"
	log.Printf("Сервер запущен на http://localhost%s", port)
	if err := http.ListenAndServe(port, r); err != nil {
		log.Fatalf("Сервер упал: %v", err)
	}
}
