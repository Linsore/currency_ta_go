// cmd/server/main.go
//
// Package main wires the HTTP server, database connection, background worker,
// and infrastructure components (cache, rate limiter, external exchanger).
// The runtime behavior is controlled via environment variables (see below).
//
// Environment variables:
//   - ADDR           : HTTP listen address (default ":8080")
//   - DATABASE_URL   : PostgreSQL DSN (default "postgres://postgres:postgres@localhost:5432/quotes?sslmode=disable")
//   - FX_BASE_URL    : Base URL for the external exchange API (default "https://api.exchangerate.host")
//   - FX_PAIRS       : Comma-separated supported pairs, e.g. "USD/EUR,EUR/USD" (default "USD/EUR,EUR/USD,EUR/MXN,USD/MXN")
//   - CACHE_TTL      : Cache TTL duration, e.g. "10m" (default "10m")
//   - RATE_LIMIT     : Requests per window per IP, e.g. "60" (default 60)
//   - RATE_WINDOW    : Window size for rate limiting, e.g. "1m" (default "1m")
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"currency_ta_go/internal/cache"
	"currency_ta_go/internal/external"
	"currency_ta_go/internal/httpx"
	"currency_ta_go/internal/ratelimit"
	"currency_ta_go/internal/repo"
	"currency_ta_go/internal/service"
)

// default configuration values
const (
	defaultAddr        = ":8080"
	defaultFXBaseURL   = "https://api.frankfurter.dev/v1"
	defaultFXPairs     = "USD/EUR,EUR/USD,EUR/MXN,USD/MXN"
	defaultCacheTTL    = 10 * time.Minute
	defaultRateLimit   = 60
	defaultRateWindow  = time.Minute
	defaultDatabaseURL = "postgres://postgres:postgres@localhost:5432/quotes?sslmode=disable"
)



func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// ----- Configuration -----
	httpAddr := getEnvOrDefault("ADDR", defaultAddr)
	databaseURL := getEnvOrDefault("DATABASE_URL", defaultDatabaseURL)
	externalAPIBaseURL := getEnvOrDefault("FX_BASE_URL", defaultFXBaseURL)
	supportedPairs := service.ParsePairs(getEnvOrDefault("FX_PAIRS", defaultFXPairs))

	cacheTTL := parseEnvDuration("CACHE_TTL", defaultCacheTTL)
	rateLimit := parseEnvInt("RATE_LIMIT", defaultRateLimit)
	rateWindow := parseEnvDuration("RATE_WINDOW", defaultRateWindow)
	
	// ----- Database Pool & Migrations -----
	ctx := context.Background()

	dbPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("failed to create DB pool: %v", err)
	}
	defer dbPool.Close()

	if err := repo.RunMigrations(ctx, dbPool); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	// ----- Infrastructure (external API, cache, repo, service) -----
	exchanger := external.New(externalAPIBaseURL, 10*time.Second)
	if strings.EqualFold(getEnvOrDefault("FX_DEBUG", ""), "1") || strings.EqualFold(getEnvOrDefault("FX_DEBUG", ""), "true") {
		exchanger.Debug = true
	}
	quotesCache := cache.New[string, service.Quote](cacheTTL)
	dataRepo := repo.New(dbPool)
	svc := service.New(supportedPairs, dataRepo, exchanger, quotesCache)

	// ----- HTTP wiring (rate limiter + logging + CORS) -----
	ipLimiter := ratelimit.New(rateLimit, rateWindow)
	mux := httpx.NewMux(svc, ipLimiter.Middleware)
	allowedOrigins := getEnvOrDefault("CORS_ALLOW_ORIGINS", "http://localhost:8081")
	cors := httpx.NewCORS(allowedOrigins)

	var handler http.Handler = mux
	handler = cors(handler)
	handler = httpx.LogRequests(handler)

	server := &http.Server{
		Addr:    httpAddr,
		Handler: handler,
	}


	go func() {
		log.Printf("listening on %s", httpAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	// ----- Graceful shutdown -----
	shutdownOnSignal(server)
}

// getEnvOrDefault returns environment variable value if set, otherwise defaultVal.
func getEnvOrDefault(key string, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func parseEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if raw := os.Getenv(key); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
		log.Printf("WARN: invalid %s=%q, using default %s", key, raw, defaultVal)
	}
	return defaultVal
}


func parseEnvInt(key string, defaultVal int) int {
	if raw := os.Getenv(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n
		}
		log.Printf("WARN: invalid %s=%q, using default %d", key, raw, defaultVal)
	}
	return defaultVal
}

func shutdownOnSignal(server *http.Server) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown error: %v", err)
	}
}
