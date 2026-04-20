# Auto-Anki (prototype)

Два бинаря:
- `cmd/api` — HTTP API (Auth, Sources, Generation Jobs, Cards, Export).
- `cmd/worker` — воркер (парсинг+чанкинг+эмбеддинги+генерация карточек).

## Быстрый старт (dev)

1) Postgres + Redis:
```bash
docker compose up -d postgres redis
```

2) Миграции:
```bash
psql "$DATABASE_URL" -f migrations/001_init.sql -f migrations/002_cards_dedup.sql
```

3) Запуск API и воркера:
```bash
export DATABASE_URL='postgres://auto_anki:auto_anki@localhost:5432/auto_anki?sslmode=disable'
export REDIS_ADDR='localhost:6379'
export JWT_SECRET='dev-secret'
export DATA_DIR='./data'

go run ./cmd/api
go run ./cmd/worker
```

⚠️ В прототипе LLM/embeddings реализованы "заглушками" (детерминированные псевдо-эмбеддинги и генератор Q/A),
чтобы проект запускался без внешнего провайдера. Подключение реального LLM — через `internal/infra/llm`.

## Retry/DLQ для Redis Streams

Воркер использует retry + DLQ для сообщений из `parse_jobs` и `gen_jobs`:

- каждое сообщение содержит поле `attempt` (начинается с `0`)
- при ошибке сообщение ACKается и перекидывается обратно в stream с `attempt+1`
- при достижении лимита попыток сообщение кладётся в `parse_jobs_dlq` / `gen_jobs_dlq`
- перед обработкой применяется экспоненциальный backoff

Переменные окружения:

- `STREAM_MAX_ATTEMPTS` (по умолчанию `5`)
- `STREAM_BACKOFF_BASE_MS` (по умолчанию `200`) — база для backoff
- `WORKER_NAME` (по умолчанию `worker-1`) — имя consumer в группе


## Crash-recovery (pending entries)

Воркер также использует `XAUTOCLAIM` для возврата сообщений из PEL (pending entries list),
если воркер упал до ACK. Порог простоя настраивается переменными:

- `PARSE_CLAIM_IDLE_MS` (по умолчанию `60000`)
- `GEN_CLAIM_IDLE_MS` (по умолчанию `300000`)
