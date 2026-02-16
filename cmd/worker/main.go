package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/example/auto-anki/internal/app/worker"
	"github.com/example/auto-anki/internal/infra/filestore"
	"github.com/example/auto-anki/internal/infra/llm"
	"github.com/example/auto-anki/internal/infra/postgres"
	"github.com/example/auto-anki/internal/infra/redisq"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := mustConfig()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr,
	})
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Printf("redis close: %v", err)
		}
	}()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis ping: %v", err)
	}

	consumer := redisq.Consumer{
		Rdb:   rdb,
		Group: "auto-anki",
		Name:  env("WORKER_NAME", "worker-1"),
	}

	if err := consumer.EnsureGroups(ctx); err != nil {
		log.Fatalf("ensure consumer groups: %v", err)
	}

	fs := filestore.LocalFileStore{BaseDir: cfg.DataDir}

	p := worker.Pipeline{
		Sources:  postgres.SourcesRepo{Pool: pool},
		Chunks:   postgres.ChunksRepo{Pool: pool},
		Jobs:     postgres.GenerationJobsRepo{Pool: pool},
		Cards:    postgres.CardsRepo{Pool: pool},
		Files:    fs,
		Embedder: llm.NewOllamaEmbedder(),
		LLM:      llm.NewOpenAICardLLM(),
		Clock:    usecase.RealClock{},
	}

	log.Printf("worker started (group=%s consumer=%s)", consumer.Group, consumer.Name)

	parseCursor := "0-0"
	genCursor := "0-0"

	for {
		select {
		case <-ctx.Done():
			log.Printf("shutdown signal received")
			return
		default:
		}

		// Один проход цикла: pending -> new для parse и gen.
		runLoopOnce(ctx, consumer, p, cfg, &parseCursor, &genCursor)

		// Небольшая пауза, чтобы не крутить CPU вхолостую.
		if !sleepCtx(ctx, cfg.LoopPause) {
			log.Printf("shutdown")
			return
		}
	}
}

func runLoopOnce(
	ctx context.Context,
	consumer redisq.Consumer,
	p worker.Pipeline,
	cfg config,
	parseCursor *string,
	genCursor *string,
) {
	// 1) PARSE pending (recovery from PEL)
	parsePending, nextParseCursor, err := consumer.ConsumeParsePending(ctx, cfg.ParseClaimIdle, *parseCursor, cfg.ParseBatchSize)
	if err != nil {
		log.Printf("claim parse pending: %v", err)
	} else {
		*parseCursor = nextParseCursor
		processBatch(
			ctx, "parse", consumer, p, parsePending, cfg,
			p.HandleParseMessage,
			consumer.AckParse,
			consumer.RetryParseOrDLQ,
		)
	}

	// 2) PARSE new
	parseMsgs, err := consumer.ConsumeParse(ctx, cfg.ReadBlock, cfg.ParseBatchSize)
	if err != nil {
		log.Printf("consume parse: %v", err)
		_ = sleepCtx(ctx, cfg.ErrorSleep)
	} else {
		processBatch(
			ctx, "parse", consumer, p, parseMsgs, cfg,
			p.HandleParseMessage,
			consumer.AckParse,
			consumer.RetryParseOrDLQ,
		)
	}

	// 3) GEN pending
	genPending, nextGenCursor, err := consumer.ConsumeGenPending(ctx, cfg.GenClaimIdle, *genCursor, cfg.GenBatchSize)
	if err != nil {
		log.Printf("claim gen pending: %v", err)
	} else {
		*genCursor = nextGenCursor
		processBatch(
			ctx, "gen", consumer, p, genPending, cfg,
			p.HandleGenMessage,
			consumer.AckGen,
			consumer.RetryGenOrDLQ,
		)
	}

	// 4) GEN new
	genMsgs, err := consumer.ConsumeGen(ctx, cfg.ReadBlock, cfg.GenBatchSize)
	if err != nil {
		log.Printf("consume gen: %v", err)
		_ = sleepCtx(ctx, cfg.ErrorSleep)
	} else {
		processBatch(
			ctx, "gen", consumer, p, genMsgs, cfg,
			p.HandleGenMessage,
			consumer.AckGen,
			consumer.RetryGenOrDLQ,
		)
	}
}

func processBatch(
	ctx context.Context,
	kind string,
	consumer redisq.Consumer,
	p worker.Pipeline,
	msgs []redisq.Message,
	cfg config,
	handle func(context.Context, string) error,
	ack func(context.Context, ...string) error,
	retry func(context.Context, redisq.Message, int, error) error,
) {
	for _, m := range msgs {
		// backoff по attempt (с jitter, чтобы несколько воркеров не ретраили синхронно)
		if d := redisq.BackoffWithJitter(m.Attempt, cfg.BackoffBase, cfg.BackoffJitter); d > 0 {
			if !sleepCtx(ctx, d) {
				return
			}
		}

		// Если контекст завершён — не ACK и не Retry.
		// Сообщение останется в PEL и будет подобрано через XAUTOCLAIM.
		if err := ctx.Err(); err != nil {
			return
		}

		err := handle(ctx, m.Payload)
		if err != nil {
			// При shutdown не пытаемся делать retry/ack — оставим в PEL для восстановления.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
				log.Printf("%s handler canceled: id=%s claimed=%t attempt=%d", kind, m.ID, m.Claimed, m.Attempt)
				return
			}

			log.Printf("%s job failed: id=%s claimed=%t attempt=%d err=%v", kind, m.ID, m.Claimed, m.Attempt, err)

			if rerr := retry(ctx, m, cfg.MaxAttempts, err); rerr != nil {
				if errors.Is(rerr, redisq.ErrMessageNotPending) {
					// Мягкая гонка: сообщение уже не в PEL (кто-то успел ack/reclaim раньше).
					log.Printf("%s retry skipped (not pending): id=%s claimed=%t attempt=%d", kind, m.ID, m.Claimed, m.Attempt)
				} else {
					log.Printf("%s retry/dlq failed: id=%s claimed=%t attempt=%d err=%v", kind, m.ID, m.Claimed, m.Attempt, rerr)
				}
			}
			continue
		}

		if err := ack(ctx, m.ID); err != nil {
			// Не фейлим процесс: сообщение останется в PEL и будет reclaimed.
			log.Printf("%s ack failed: id=%s claimed=%t attempt=%d err=%v", kind, m.ID, m.Claimed, m.Attempt, err)
			continue
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type config struct {
	DatabaseURL string
	RedisAddr   string
	DataDir     string

	MaxAttempts int

	BackoffBase   time.Duration
	BackoffJitter float64

	ParseClaimIdle time.Duration
	GenClaimIdle   time.Duration

	ReadBlock  time.Duration
	ErrorSleep time.Duration
	LoopPause  time.Duration

	ParseBatchSize int
	GenBatchSize   int
}

func mustConfig() config {
	c := config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisAddr:   env("REDIS_ADDR", "localhost:6379"),
		DataDir:     env("DATA_DIR", "./data"),

		MaxAttempts: envInt("STREAM_MAX_ATTEMPTS", 5),

		BackoffBase:   time.Duration(envInt("STREAM_BACKOFF_BASE_MS", 200)) * time.Millisecond,
		BackoffJitter: envFloat("STREAM_BACKOFF_JITTER", 0.2),

		ParseClaimIdle: time.Duration(envInt("PARSE_CLAIM_IDLE_MS", 60_000)) * time.Millisecond,
		GenClaimIdle:   time.Duration(envInt("GEN_CLAIM_IDLE_MS", 300_000)) * time.Millisecond,

		ReadBlock:  time.Duration(envInt("STREAM_READ_BLOCK_MS", 2_000)) * time.Millisecond,
		ErrorSleep: time.Duration(envInt("STREAM_ERROR_SLEEP_MS", 250)) * time.Millisecond,
		LoopPause:  time.Duration(envInt("STREAM_LOOP_PAUSE_MS", 50)) * time.Millisecond,

		ParseBatchSize: envInt("PARSE_BATCH_SIZE", 20),
		GenBatchSize:   envInt("GEN_BATCH_SIZE", 10),
	}

	if c.DatabaseURL == "" {
		log.Fatalf("DATABASE_URL is required")
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.ParseBatchSize <= 0 {
		c.ParseBatchSize = 20
	}
	if c.GenBatchSize <= 0 {
		c.GenBatchSize = 10
	}
	if c.BackoffJitter < 0 {
		c.BackoffJitter = 0
	}
	return c
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("invalid int env %s=%q, using default=%d", k, v, def)
		return def
	}
	return n
}

func envFloat(k string, def float64) float64 {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		log.Printf("invalid float env %s=%q, using default=%f", k, v, def)
		return def
	}
	return n
}
