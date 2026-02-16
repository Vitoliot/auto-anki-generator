package redisq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	streamParse    = "parse_jobs"
	streamGen      = "gen_jobs"
	streamParseDLQ = "parse_jobs_dlq"
	streamGenDLQ   = "gen_jobs_dlq"
)

var (
	// ErrMessageNotPending возникает, если во время retry/DLQ сообщение уже не находится в PEL
	// (например, гонка с другим consumer/reclaimer).
	ErrMessageNotPending = errors.New("message is not pending (already acked/claimed by another consumer)")
)

func init() {
	// Для jitter в BackoffWithJitter.
	rand.Seed(time.Now().UnixNano())
}

type Queue struct {
	Rdb *redis.Client
}

func (q Queue) EnqueueParseSource(ctx context.Context, sourceID uuid.UUID) error {
	b, err := json.Marshal(map[string]string{"source_id": sourceID.String()})
	if err != nil {
		return fmt.Errorf("marshal parse payload: %w", err)
	}

	return q.Rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: streamParse,
		Values: map[string]any{
			"payload": string(b),
			"attempt": 0,
		},
	}).Err()
}

func (q Queue) EnqueueGenerateCards(ctx context.Context, jobID uuid.UUID) error {
	b, err := json.Marshal(map[string]string{"job_id": jobID.String()})
	if err != nil {
		return fmt.Errorf("marshal gen payload: %w", err)
	}

	return q.Rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: streamGen,
		Values: map[string]any{
			"payload": string(b),
			"attempt": 0,
		},
	}).Err()
}

type Consumer struct {
	Rdb   *redis.Client
	Group string
	Name  string
}

type Message struct {
	ID      string
	Payload string
	Attempt int
	Claimed bool // true if obtained via XAUTOCLAIM (recovery of pending entries)
}

// EnsureGroups creates consumer groups if they do not exist.
// Игнорирует только BUSYGROUP (группа уже существует).
func (c Consumer) EnsureGroups(ctx context.Context) error {
	if err := c.ensureGroup(ctx, streamParse); err != nil {
		return err
	}
	if err := c.ensureGroup(ctx, streamGen); err != nil {
		return err
	}
	return nil
}

func (c Consumer) ensureGroup(ctx context.Context, stream string) error {
	err := c.Rdb.XGroupCreateMkStream(ctx, stream, c.Group, "0").Err()
	if err == nil {
		return nil
	}

	// go-redis часто возвращает Redis error как обычную строку.
	if strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}

	return fmt.Errorf("create consumer group %q for stream %q: %w", c.Group, stream, err)
}

func (c Consumer) ConsumeParse(ctx context.Context, block time.Duration, count int) ([]Message, error) {
	return c.consumeNew(ctx, streamParse, block, count)
}

func (c Consumer) ConsumeGen(ctx context.Context, block time.Duration, count int) ([]Message, error) {
	return c.consumeNew(ctx, streamGen, block, count)
}

// ConsumeParsePending reclaims messages stuck in PEL (pending entries list) after crashes.
// start is an XAUTOCLAIM cursor (use "0-0" initially). The returned nextStart can be reused.
func (c Consumer) ConsumeParsePending(ctx context.Context, minIdle time.Duration, start string, count int) ([]Message, string, error) {
	return c.consumePending(ctx, streamParse, minIdle, start, count)
}

func (c Consumer) ConsumeGenPending(ctx context.Context, minIdle time.Duration, start string, count int) ([]Message, string, error) {
	return c.consumePending(ctx, streamGen, minIdle, start, count)
}

func (c Consumer) AckParse(ctx context.Context, ids ...string) error {
	return c.ack(ctx, streamParse, ids...)
}

func (c Consumer) AckGen(ctx context.Context, ids ...string) error {
	return c.ack(ctx, streamGen, ids...)
}

func (c Consumer) consumeNew(ctx context.Context, stream string, block time.Duration, count int) ([]Message, error) {
	res, err := c.Rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.Group,
		Consumer: c.Name,
		Streams:  []string{stream, ">"},
		Count:    int64(count),
		Block:    block,
	}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("xreadgroup stream=%s group=%s consumer=%s: %w", stream, c.Group, c.Name, err)
	}

	var out []Message
	for _, s := range res {
		for _, m := range s.Messages {
			out = append(out, parseMsg(m, false))
		}
	}
	return out, nil
}

func (c Consumer) consumePending(ctx context.Context, stream string, minIdle time.Duration, start string, count int) ([]Message, string, error) {
	if start == "" {
		start = "0-0"
	}

	cmd := c.Rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   stream,
		Group:    c.Group,
		Consumer: c.Name,
		MinIdle:  minIdle,
		Start:    start,
		Count:    int64(count),
	})
	res, nextStart, err := cmd.Result()
	if errors.Is(err, redis.Nil) {
		return nil, nextStart, nil
	}
	if err != nil {
		return nil, nextStart, fmt.Errorf("xautoclaim stream=%s group=%s consumer=%s start=%s: %w",
			stream, c.Group, c.Name, start, err)
	}

	var out []Message
	for _, m := range res {
		out = append(out, parseMsg(m, true))
	}
	return out, nextStart, nil
}

func parseMsg(m redis.XMessage, claimed bool) Message {
	payload, _ := m.Values["payload"].(string)

	att := 0
	switch v := m.Values["attempt"].(type) {
	case int64:
		att = int(v)
	case int:
		att = v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			att = n
		}
	}

	return Message{
		ID:      m.ID,
		Payload: payload,
		Attempt: att,
		Claimed: claimed,
	}
}

func (c Consumer) ack(ctx context.Context, stream string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := c.Rdb.XAck(ctx, stream, c.Group, ids...).Err(); err != nil {
		return fmt.Errorf("xack stream=%s group=%s ids=%v: %w", stream, c.Group, ids, err)
	}
	return nil
}

var _ usecase.Queue = Queue{}

// PermanentError marks an error as non-retryable (go directly to DLQ).
type PermanentError struct{ Err error }

func (e PermanentError) Error() string {
	if e.Err == nil {
		return "permanent error"
	}
	return e.Err.Error()
}

func (e PermanentError) Unwrap() error { return e.Err }

// MarkPermanent wraps an error so RetryOrDLQ sends it directly to DLQ.
func MarkPermanent(err error) error {
	if err == nil {
		return nil
	}
	return PermanentError{Err: err}
}

func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	var pe PermanentError
	return errors.As(err, &pe)
}

func errorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case IsPermanent(err):
		return "permanent"
	default:
		return "transient"
	}
}

// Retry/DLQ helpers.

// RetryOrDLQ atomically ACKs the original message and re-enqueues it with incremented attempt,
// or moves to DLQ when maxAttempts is reached / error is permanent.
//
// ВАЖНО:
// - Семантика доставки всё равно at-least-once -> обработчик должен быть идемпотентным.
// - ACK + XADD делаются атомарно через Lua, чтобы не потерять сообщение между шагами.
func (c Consumer) RetryOrDLQ(ctx context.Context, stream string, msg Message, maxAttempts int, cause error) error {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	nextAttempt := msg.Attempt + 1
	permanent := IsPermanent(cause)

	errStr := ""
	if cause != nil {
		errStr = cause.Error()
	}

	toDLQ := permanent || nextAttempt >= maxAttempts

	now := time.Now().UTC()
	dstStream := stream
	values := map[string]any{
		"payload":       msg.Payload,
		"attempt":       nextAttempt,
		"last_error":    errStr,
		"error_kind":    errorKind(cause),
		"claimed":       boolToInt(msg.Claimed),
		"requeued_at":   now.Format(time.RFC3339Nano),
		"requeued_unix": now.UnixMilli(),
	}

	if toDLQ {
		if stream == streamGen {
			dstStream = streamGenDLQ
		} else {
			dstStream = streamParseDLQ
		}
		values = map[string]any{
			"payload":        msg.Payload,
			"attempt":        nextAttempt,
			"error":          errStr,
			"error_kind":     errorKind(cause),
			"claimed":        boolToInt(msg.Claimed),
			"failed_at":      now.Format(time.RFC3339Nano),
			"failed_at_unix": now.UnixMilli(),
		}
	}

	if err := c.ackAndAddAtomic(ctx, stream, dstStream, msg.ID, values); err != nil {
		return fmt.Errorf("retry/dlq stream=%s msg=%s dst=%s attempt=%d: %w",
			stream, msg.ID, dstStream, nextAttempt, err)
	}

	return nil
}

func (c Consumer) RetryParseOrDLQ(ctx context.Context, msg Message, maxAttempts int, err error) error {
	return c.RetryOrDLQ(ctx, streamParse, msg, maxAttempts, err)
}

func (c Consumer) RetryGenOrDLQ(ctx context.Context, msg Message, maxAttempts int, err error) error {
	return c.RetryOrDLQ(ctx, streamGen, msg, maxAttempts, err)
}

// Lua script: atomically XACK from source stream PEL and XADD to destination stream.
// Returns new stream message ID on success, nil if XACK returned 0 (not pending).
var ackAndAddScript = redis.NewScript(`
local acked = redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
if acked == 0 then
  return nil
end

local xaddArgs = {'XADD', KEYS[2], '*'}
for i = 3, #ARGV do
  table.insert(xaddArgs, ARGV[i])
end

return redis.call(unpack(xaddArgs))
`)

// ackAndAddAtomic performs XACK (source stream) + XADD (dst stream) atomically.
// values are encoded as field/value pairs for XADD.
// NOTE: In Redis Cluster, src/dst keys must be in the same hash slot for script/transaction semantics across keys.
// For that use hash tags in keys, e.g. queue:{parse}:main / queue:{parse}:dlq.
func (c Consumer) ackAndAddAtomic(ctx context.Context, srcStream, dstStream, msgID string, values map[string]any) error {
	if srcStream == "" || dstStream == "" {
		return fmt.Errorf("empty stream name")
	}
	if msgID == "" {
		return fmt.Errorf("empty message id")
	}
	if c.Group == "" {
		return fmt.Errorf("empty consumer group")
	}
	if len(values) == 0 {
		return fmt.Errorf("empty xadd values")
	}

	// Стабильный порядок полей -> полезно для отладки/тестов.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	argv := make([]any, 0, 2+2*len(values))
	argv = append(argv, c.Group, msgID)
	for _, k := range keys {
		argv = append(argv, k, fmt.Sprint(values[k]))
	}

	res, err := ackAndAddScript.Run(ctx, c.Rdb, []string{srcStream, dstStream}, argv...).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrMessageNotPending
		}
		return err
	}
	if res == nil {
		return ErrMessageNotPending
	}

	return nil
}

// Backoff returns exponential backoff derived from attempt.
func Backoff(attempt int, base time.Duration) time.Duration {
	if attempt <= 0 {
		return 0
	}
	if base <= 0 {
		base = 200 * time.Millisecond
	}
	// cap to avoid runaway
	if attempt > 10 {
		attempt = 10
	}
	return base * time.Duration(1<<attempt)
}

// BackoffWithJitter adds jitter to exponential backoff to reduce thundering herd.
// jitterRatio=0.2 means +[0..20%) random delay.
func BackoffWithJitter(attempt int, base time.Duration, jitterRatio float64) time.Duration {
	d := Backoff(attempt, base)
	if d <= 0 {
		return 0
	}
	if jitterRatio <= 0 {
		jitterRatio = 0.2
	}

	maxJitter := int64(float64(d) * jitterRatio)
	if maxJitter <= 0 {
		return d
	}

	return d + time.Duration(rand.Int63n(maxJitter))
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
