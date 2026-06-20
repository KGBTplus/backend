package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
<<<<<<< HEAD
=======
	"sync"
>>>>>>> date-+++
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

<<<<<<< HEAD
=======
// Simple in-memory rate limiter
type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]int
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		visitors: make(map[string]int),
		limit:    limit,
		window:   window,
	}
	go rl.cleanup()
	return rl
}

func (rl *rateLimiter) cleanup() {
	for {
		time.Sleep(rl.window)
		rl.mu.Lock()
		rl.visitors = make(map[string]int)
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	count := rl.visitors[key]
	if count >= rl.limit {
		return false
	}
	rl.visitors[key] = count + 1
	return true
}

>>>>>>> date-+++
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
<<<<<<< HEAD
	jwtSecret := getEnv("JWT_SECRET", "my_secret_key")
	srv := api.NewServer(queries, smtpCfg, jwtSecret)
=======
	jwtSecret := getEnv("JWT_SECRET", "")
	if len(jwtSecret) < 32 {
		log.Fatal("JWT_SECRET должен быть не менее 32 символов. Задайте переменную окружения JWT_SECRET.")
	}
	secureCookies := os.Getenv("ENV") == "production" || os.Getenv("SECURE_COOKIES") == "true"
	srv := api.NewServer(queries, smtpCfg, jwtSecret, api.WithSecureCookies(secureCookies))

	// Rate limiter: 10 запросов в секунду на IP для auth
	authLimiter := newRateLimiter(10, time.Second)
>>>>>>> date-+++

	// 4. Настройка роутера
	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
<<<<<<< HEAD
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
=======
		AllowedOrigins:   []string{"http://localhost:8080", "http://localhost:5173", "https://team4.verstack.ru"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Безопасность: заголовки
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:")
			next.ServeHTTP(w, r)
		})
	})

	// Проверка Origin для защиты от CSRF
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			// Разрешаем POST-запросы без Origin только от тех же хостов
			if r.Method == "POST" && origin != "" {
				allowedOrigins := []string{"http://localhost:8080", "http://localhost:5173", "https://team4.verstack.ru"}
				ok := false
				for _, o := range allowedOrigins {
					if o == origin {
						ok = true
						break
					}
				}
				if !ok {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
>>>>>>> date-+++
			}
			next.ServeHTTP(w, r)
		})
	})

<<<<<<< HEAD
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
=======
	// 5. API руты (auth endpoints under rate limiter)
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ip := r.RemoteAddr
				if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
					ip = forwarded
				}
				if !authLimiter.Allow("auth:" + ip) {
					http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
					return
				}
				next.ServeHTTP(w, r)
			})
		})
		api.HandlerFromMux(srv, r)
		r.Post("/auth/verify-email", srv.VerifyEmail)
		r.Post("/auth/verify-otp", srv.VerifyOTP)
		r.Post("/auth/password/forgot/send-code", srv.SendForgotPasswordCode)
		r.Post("/auth/password/forgot/reset", srv.ResetForgotPassword)
		r.Get("/auth/me", srv.AuthMe)
		r.Post("/auth/refresh", srv.RefreshToken)
		r.Get("/auth/ws-token", srv.WsToken)
		r.Post("/auth/logout", srv.Logout)
	})

	r.Get("/ws", srv.HandleWebSocket)

	r.Get("/shop", srv.GetShop)
	r.Post("/buy_fish", srv.BuyFish)
	r.Post("/equip_fish", srv.EquipFish)
	r.Get("/swagger/doc.json", func(w http.ResponseWriter, r *http.Request) {
		spec, err := api.GetSpecJSON()
		if err != nil {
			log.Printf("Ошибка получения swagger spec: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
>>>>>>> date-+++
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
