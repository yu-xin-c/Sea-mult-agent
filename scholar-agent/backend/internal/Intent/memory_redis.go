package Intent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type StoredTurn struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	TsMs       int64          `json:"ts_ms"`
	IntentType string         `json:"intent_type,omitempty"`
	Entities   map[string]any `json:"entities,omitempty"`
}

func (t StoredTurn) toMemoryTurn() MemoryTurn {
	return MemoryTurn{
		Role:    t.Role,
		Content: t.Content,
	}
}

type MemoryStore interface {
	Enabled() bool
	EnsureSession(ctx context.Context, userID, sessionID string, ttl time.Duration) error
	AppendTurn(ctx context.Context, sessionID string, turn StoredTurn, maxTurns int, ttl time.Duration) error
	LoadRecentTurns(ctx context.Context, sessionID string, limit int) ([]StoredTurn, error)
}

type NoopMemoryStore struct{}

func (s *NoopMemoryStore) Enabled() bool { return false }

func (s *NoopMemoryStore) EnsureSession(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}

func (s *NoopMemoryStore) AppendTurn(_ context.Context, _ string, _ StoredTurn, _ int, _ time.Duration) error {
	return nil
}

func (s *NoopMemoryStore) LoadRecentTurns(_ context.Context, _ string, _ int) ([]StoredTurn, error) {
	return nil, nil
}

type RedisMemoryStore struct {
	client *redis.Client
	prefix string
}

func NewRedisMemoryStoreFromEnv() (MemoryStore, error) {
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if addr == "" {
		return &NoopMemoryStore{}, nil
	}

	opt := &redis.Options{
		Addr:     addr,
		Password: strings.TrimSpace(os.Getenv("REDIS_PASSWORD")),
	}
	if user := strings.TrimSpace(os.Getenv("REDIS_USERNAME")); user != "" {
		opt.Username = user
	}
	if dbRaw := strings.TrimSpace(os.Getenv("REDIS_DB")); dbRaw != "" {
		if db, err := strconv.Atoi(dbRaw); err == nil && db >= 0 {
			opt.DB = db
		}
	}

	return &RedisMemoryStore{
		client: redis.NewClient(opt),
		prefix: "sa:intent:",
	}, nil
}

func (s *RedisMemoryStore) Enabled() bool { return s != nil && s.client != nil }

func (s *RedisMemoryStore) EnsureSession(ctx context.Context, userID, sessionID string, ttl time.Duration) error {
	if !s.Enabled() || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	now := time.Now().UnixMilli()
	key := s.sessionKey(sessionID)
	pipe := s.client.Pipeline()
	pipe.HSetNX(ctx, key, "user_id", userID)
	pipe.HSetNX(ctx, key, "created_at", now)
	pipe.HSet(ctx, key, "updated_at", now)
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisMemoryStore) AppendTurn(ctx context.Context, sessionID string, turn StoredTurn, maxTurns int, ttl time.Duration) error {
	if !s.Enabled() || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if turn.TsMs <= 0 {
		turn.TsMs = time.Now().UnixMilli()
	}
	if maxTurns <= 0 {
		maxTurns = 30
	}
	raw, err := json.Marshal(turn)
	if err != nil {
		return fmt.Errorf("marshal turn: %w", err)
	}

	turnsKey := s.turnsKey(sessionID)
	sessionKey := s.sessionKey(sessionID)
	pipe := s.client.Pipeline()
	pipe.LPush(ctx, turnsKey, string(raw))
	pipe.LTrim(ctx, turnsKey, 0, int64(maxTurns-1))
	if ttl > 0 {
		pipe.Expire(ctx, turnsKey, ttl)
		pipe.Expire(ctx, sessionKey, ttl)
	}
	_, execErr := pipe.Exec(ctx)
	return execErr
}

func (s *RedisMemoryStore) LoadRecentTurns(ctx context.Context, sessionID string, limit int) ([]StoredTurn, error) {
	if !s.Enabled() || strings.TrimSpace(sessionID) == "" || limit <= 0 {
		return nil, nil
	}
	items, err := s.client.LRange(ctx, s.turnsKey(sessionID), 0, int64(limit-1)).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}

	turns := make([]StoredTurn, 0, len(items))
	for _, item := range items {
		var turn StoredTurn
		if unmarshalErr := json.Unmarshal([]byte(item), &turn); unmarshalErr != nil {
			continue
		}
		turns = append(turns, turn)
	}

	// Redis 通过 LPUSH 写入，读取结果是从新到旧，这里翻转为时间正序。
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return turns, nil
}

func (s *RedisMemoryStore) sessionKey(sessionID string) string {
	return s.prefix + "sess:" + sessionID
}

func (s *RedisMemoryStore) turnsKey(sessionID string) string {
	return s.prefix + "turns:" + sessionID
}
