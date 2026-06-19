package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/KGBTplus/backend/internal/api"
	"github.com/KGBTplus/backend/internal/db"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	swagger "github.com/swaggo/http-swagger/v2"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
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

	// Подождём, пока БД станет доступна (на случай задержки DNS/стартов контейнеров)
	maxAttempts := 30
	for i := 0; i < maxAttempts; i++ {
		if err := dbConn.Ping(); err == nil {
			break
		}
		log.Printf("Ожидание БД (%d/%d): %v", i+1, maxAttempts, dbConn.Ping())
		time.Sleep(2 * time.Second)
	}

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
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
	}))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	
	// CORS middleware
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	// 5. API руты
	api.HandlerFromMux(srv, r)
	r.Post("/auth/verify-email", srv.VerifyEmail)
	r.Post("/auth/verify-otp", srv.VerifyOTP)
	r.Post("/login", srv.Login)
	r.Post("/register", srv.Register)
	r.Post("/verify-otp", srv.VerifyOTP)

	r.Post("/auth/password/forgot/send-code", srv.SendForgotPasswordCode)
	r.Post("/auth/password/forgot/reset", srv.ResetForgotPassword)

	r.Get("/ws", srv.HandleWebSocket)

	r.Get("/swagger/doc.json", func(w http.ResponseWriter, r *http.Request) {
		spec, err := api.GetSpecJSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(spec)
	})
	r.Get("/swagger/*", swagger.Handler(
		swagger.URL("/swagger/doc.json"),
	))

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
