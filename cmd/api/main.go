package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/example/auto-anki/internal/infra/auth"
	"github.com/example/auto-anki/internal/infra/export"
	"github.com/example/auto-anki/internal/infra/filestore"
	"github.com/example/auto-anki/internal/infra/postgres"
	"github.com/example/auto-anki/internal/infra/redisq"
	"github.com/example/auto-anki/internal/transport/http/handler"
	"github.com/example/auto-anki/internal/transport/http/middleware"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx := context.Background()

	cfg := mustConfig()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	q := redisq.Queue{Rdb: rdb}
	fs := filestore.LocalFileStore{BaseDir: cfg.DataDir}

	signer := auth.Signer{Secret: []byte(cfg.JWTSecret), TTL: 24 * time.Hour}
	mw := middleware.AuthMiddleware{Signer: signer}

	authSvc := usecase.AuthService{
		Users:  postgres.UsersRepo{Pool: pool},
		Clock:  usecase.RealClock{},
		Signer: signer,
	}

	srcSvc := usecase.SourceService{
		Sources: postgres.SourcesRepo{Pool: pool},
		Queue:   q,
		Clock:   usecase.RealClock{},
		Files:   fs,
	}

	genSvc := usecase.GenerationService{
		Sources:  postgres.SourcesRepo{Pool: pool},
		CardSets: postgres.CardSetsRepo{Pool: pool},
		Jobs:     postgres.GenerationJobsRepo{Pool: pool},
		Queue:    q,
		Clock:    usecase.RealClock{},
	}

	cardSvc := usecase.CardService{Cards: postgres.CardsRepo{Pool: pool}}

	expSvc := usecase.ExportService{
		Exports:  postgres.ExportJobsRepo{Pool: pool},
		Cards:    postgres.CardsRepo{Pool: pool},
		Links:    postgres.CardSourceLinksRepo{Pool: pool},
		Exporter: export.CSVExporter{},
		Clock:    usecase.RealClock{},
		Files:    fs,
	}

	h := handler.Handler{
		Auth:       authSvc,
		Sources:    srcSvc,
		Generation: genSvc,
		Cards:      cardSvc,
		Export:     expSvc,
	}

	mux := http.NewServeMux()
	mux.Handle("/", h.Routes(mw))
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("api listening on %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http: %v", err)
	}
}

type config struct {
	HTTPAddr    string
	DatabaseURL string
	RedisAddr   string
	JWTSecret   string
	DataDir     string
}

func mustConfig() config {
	c := config{
		HTTPAddr:    env("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisAddr:   env("REDIS_ADDR", "localhost:6379"),
		JWTSecret:   env("JWT_SECRET", "dev-secret"),
		DataDir:     env("DATA_DIR", "./data"),
	}
	if c.DatabaseURL == "" {
		log.Fatalf("DATABASE_URL is required")
	}
	return c
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
