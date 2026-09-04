// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package recentalert 保存 Elasticsearch 最近写入的 Alert 快照，用于跨越搜索 refresh 可见性窗口。
package recentalert

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/store"
)

const (
	// SchemaVersion 是 Recent Alert 缓存文档的当前格式版本。
	SchemaVersion = 1
	// RefreshSafetyMargin 是 Active Alert refresh_interval 之外预留的可见性安全余量。
	RefreshSafetyMargin = 5 * time.Second
	maxEntryBytes       = 2 << 20
)

var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Config 定义 Recent Alert 缓存的派生 key 前缀和 Active Alert refresh_interval。
type Config struct {
	KeyPrefix       string
	RefreshInterval time.Duration
}

// TTL 返回 refresh_interval 加五秒安全余量后的缓存有效期。
func (c Config) TTL() time.Duration {
	return c.RefreshInterval + RefreshSafetyMargin
}

// Validate 校验缓存边界；refresh_interval 必须为正整数秒。
func (c Config) Validate() error {
	if strings.TrimSpace(c.KeyPrefix) != c.KeyPrefix || c.KeyPrefix == "" || len(c.KeyPrefix) > 512 {
		return fmt.Errorf("recent alert cache key prefix must be 1 to 512 bytes without surrounding whitespace")
	}
	if c.RefreshInterval < time.Second || c.RefreshInterval%time.Second != 0 {
		return fmt.Errorf("recent alert cache refresh interval must be whole seconds and positive")
	}
	return nil
}

// Observer 记录低基数缓存操作结果。
type Observer interface {
	Operation(ctx context.Context, operation, outcome string)
}

type noopObserver struct{}

func (noopObserver) Operation(context.Context, string, string) {}

type redisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
}

type transactionWriter func(
	ctx context.Context,
	firstKey string,
	firstValue []byte,
	secondKey string,
	secondValue []byte,
	ttl time.Duration,
) error

// Store 使用 Redis String 保存 refresh_interval 加五秒安全余量内写入的完整 StoredAlert。
// current key 解决 active 查询可见性，ended key 保留等级升级和终态部分成功恢复锚点。
type Store struct {
	client      redisClient
	transaction transactionWriter
	config      Config
	observer    Observer
}

// NewStore 创建跨 Lifecycle 实例共享的 Recent Alert 缓存。
func NewStore(client redis.UniversalClient, config Config, observer Observer) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("create recent alert cache: redis client must not be nil")
	}
	transaction := func(
		ctx context.Context,
		firstKey string,
		firstValue []byte,
		secondKey string,
		secondValue []byte,
		ttl time.Duration,
	) error {
		_, err := client.TxPipelined(ctx, func(pipeline redis.Pipeliner) error {
			pipeline.Set(ctx, firstKey, firstValue, ttl)
			pipeline.Set(ctx, secondKey, secondValue, ttl)
			return nil
		})
		return err
	}
	return newStore(client, transaction, config, observer)
}

func newStore(
	client redisClient,
	transaction transactionWriter,
	config Config,
	observer Observer,
) (*Store, error) {
	if client == nil || transaction == nil {
		return nil, fmt.Errorf("create recent alert cache: redis operations must not be nil")
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("create recent alert cache: %w", err)
	}
	if observer == nil {
		observer = noopObserver{}
	}
	return &Store{client: client, transaction: transaction, config: config, observer: observer}, nil
}

// GetCurrent 返回关联键最近写入的 active 或 terminal Alert。
// found=false 仅表示 key 明确不存在；Redis 和解码错误必须由调用方重试。
func (s *Store) GetCurrent(
	ctx context.Context,
	key store.ActiveAlertKey,
) (stored store.StoredAlert, found bool, err error) {
	cacheKey, err := s.currentKey(key)
	if err != nil {
		s.observer.Operation(ctx, "get_current", "invalid")
		return store.StoredAlert{}, false, err
	}
	stored, found, err = s.get(ctx, cacheKey, "get_current")
	if err != nil || !found {
		return stored, found, err
	}
	if stored.Alert.BKTenantID != key.BKTenantID || stored.Alert.EventSourceID != key.EventSourceID ||
		stored.Alert.Fingerprint != key.Fingerprint {
		s.observer.Operation(ctx, "get_current", "invalid")
		return store.StoredAlert{}, false, fmt.Errorf("recent alert cache current entry has mismatched identity")
	}
	return stored, true, nil
}

// GetEndedByEvent 返回最近由指定 Event 终结的 Alert。
func (s *Store) GetEndedByEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (stored store.StoredAlert, found bool, err error) {
	cacheKey, err := s.endedKey(bkTenantID, eventID)
	if err != nil {
		s.observer.Operation(ctx, "get_ended", "invalid")
		return store.StoredAlert{}, false, err
	}
	stored, found, err = s.get(ctx, cacheKey, "get_ended")
	if err != nil || !found {
		return stored, found, err
	}
	if stored.Alert.BKTenantID != bkTenantID || stored.Alert.LatestEventID != eventID ||
		!recoverableTerminal(stored.Alert) {
		s.observer.Operation(ctx, "get_ended", "invalid")
		return store.StoredAlert{}, false, fmt.Errorf("recent alert cache ended entry has mismatched identity")
	}
	return stored, true, nil
}

// PutCurrent 缓存刚创建或更新的当前 Alert。
func (s *Store) PutCurrent(ctx context.Context, stored store.StoredAlert) error {
	return s.putCurrent(ctx, stored, "put_current")
}

// PutEnded 缓存从 Elasticsearch 回源得到的终态恢复锚点，不覆盖 current key。
func (s *Store) PutEnded(ctx context.Context, stored store.StoredAlert) error {
	payload, err := encode(stored)
	if err != nil || !recoverableTerminal(stored.Alert) {
		s.observer.Operation(ctx, "put_ended", "invalid")
		if err != nil {
			return err
		}
		return fmt.Errorf("recent alert cache ended entry must be a recoverable terminal alert")
	}
	key, err := s.endedKey(stored.Alert.BKTenantID, stored.Alert.LatestEventID)
	if err != nil {
		s.observer.Operation(ctx, "put_ended", "invalid")
		return err
	}
	if err := s.client.Set(ctx, key, payload, s.config.TTL()).Err(); err != nil {
		s.observer.Operation(ctx, "put_ended", "failed")
		return fmt.Errorf("write recent alert ended cache: %w", err)
	}
	s.observer.Operation(ctx, "put_ended", "succeeded")
	return nil
}

// PutTerminal 原子缓存 terminal current 和按 Event 查询的恢复锚点。
func (s *Store) PutTerminal(ctx context.Context, stored store.StoredAlert) error {
	return s.putTerminalAs(ctx, stored, "put_terminal")
}

// Repair 用 realtime GET 的结果覆盖冲突版本，供下一次 Lifecycle 裁决使用。
func (s *Store) Repair(ctx context.Context, stored store.StoredAlert) error {
	if recoverableTerminal(stored.Alert) {
		return s.putTerminalAs(ctx, stored, "repair")
	}
	return s.putCurrent(ctx, stored, "repair")
}

func (s *Store) putCurrent(ctx context.Context, stored store.StoredAlert, operation string) error {
	payload, err := encode(stored)
	if err != nil {
		s.observer.Operation(ctx, operation, "invalid")
		return err
	}
	key, err := s.currentKey(activeKey(stored.Alert))
	if err != nil {
		s.observer.Operation(ctx, operation, "invalid")
		return err
	}
	if err := s.client.Set(ctx, key, payload, s.config.TTL()).Err(); err != nil {
		s.observer.Operation(ctx, operation, "failed")
		return fmt.Errorf("write recent alert current cache: %w", err)
	}
	s.observer.Operation(ctx, operation, "succeeded")
	return nil
}

func (s *Store) putTerminalAs(ctx context.Context, stored store.StoredAlert, operation string) error {
	payload, err := encode(stored)
	if err != nil || !recoverableTerminal(stored.Alert) {
		s.observer.Operation(ctx, operation, "invalid")
		if err != nil {
			return err
		}
		return fmt.Errorf("recent alert cache terminal entry must be recoverable")
	}
	currentKey, err := s.currentKey(activeKey(stored.Alert))
	if err != nil {
		s.observer.Operation(ctx, operation, "invalid")
		return err
	}
	endedKey, err := s.endedKey(stored.Alert.BKTenantID, stored.Alert.LatestEventID)
	if err != nil {
		s.observer.Operation(ctx, operation, "invalid")
		return err
	}
	if err := s.transaction(ctx, currentKey, payload, endedKey, payload, s.config.TTL()); err != nil {
		s.observer.Operation(ctx, operation, "failed")
		return fmt.Errorf("write recent alert terminal cache: %w", err)
	}
	s.observer.Operation(ctx, operation, "succeeded")
	return nil
}

func (s *Store) get(ctx context.Context, key, operation string) (store.StoredAlert, bool, error) {
	value, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		s.observer.Operation(ctx, operation, "miss")
		return store.StoredAlert{}, false, nil
	}
	if err != nil {
		s.observer.Operation(ctx, operation, "failed")
		return store.StoredAlert{}, false, fmt.Errorf("read recent alert cache: %w", err)
	}
	stored, err := decode(value)
	if err != nil {
		s.observer.Operation(ctx, operation, "decode_failed")
		return store.StoredAlert{}, false, err
	}
	s.observer.Operation(ctx, operation, "hit")
	return stored, true, nil
}

type document struct {
	SchemaVersion int          `json:"schema_version"`
	Alert         domain.Alert `json:"alert"`
	Version       string       `json:"version"`
}

func encode(stored store.StoredAlert) ([]byte, error) {
	alert, err := stored.Alert.Normalize()
	if err != nil {
		return nil, fmt.Errorf("encode recent alert cache: %w", err)
	}
	if stored.Version.IsZero() {
		return nil, fmt.Errorf("encode recent alert cache: version must not be empty")
	}
	payload, err := json.Marshal(document{SchemaVersion: SchemaVersion, Alert: alert, Version: stored.Version.String()})
	if err != nil {
		return nil, fmt.Errorf("encode recent alert cache: %w", err)
	}
	if len(payload) > maxEntryBytes {
		return nil, fmt.Errorf("encode recent alert cache: entry exceeds %d bytes", maxEntryBytes)
	}
	return payload, nil
}

func decode(payload []byte) (store.StoredAlert, error) {
	if len(payload) == 0 || len(payload) > maxEntryBytes {
		return store.StoredAlert{}, fmt.Errorf("decode recent alert cache: entry size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var cached document
	if err := decoder.Decode(&cached); err != nil {
		return store.StoredAlert{}, fmt.Errorf("decode recent alert cache: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return store.StoredAlert{}, fmt.Errorf("decode recent alert cache: trailing JSON data")
		}
		return store.StoredAlert{}, fmt.Errorf("decode recent alert cache trailing data: %w", err)
	}
	if cached.SchemaVersion != SchemaVersion || cached.Version == "" {
		return store.StoredAlert{}, fmt.Errorf("decode recent alert cache: schema version or storage version is invalid")
	}
	alert, err := cached.Alert.Normalize()
	if err != nil {
		return store.StoredAlert{}, fmt.Errorf("decode recent alert cache: %w", err)
	}
	return store.StoredAlert{Alert: alert, Version: store.NewVersionToken(cached.Version)}, nil
}

func (s *Store) currentKey(key store.ActiveAlertKey) (string, error) {
	if strings.TrimSpace(key.BKTenantID) == "" || strings.TrimSpace(key.EventSourceID) == "" ||
		strings.TrimSpace(key.Fingerprint) == "" {
		return "", fmt.Errorf("recent alert cache active identity must not be empty")
	}
	return s.config.KeyPrefix + ":current:" + mailbox.CorrelationKey(
		key.BKTenantID,
		key.EventSourceID,
		key.Fingerprint,
	), nil
}

func (s *Store) endedKey(bkTenantID, eventID string) (string, error) {
	if strings.TrimSpace(bkTenantID) == "" || strings.TrimSpace(eventID) == "" {
		return "", fmt.Errorf("recent alert cache ended identity must not be empty")
	}
	digest := sha256.New()
	for _, value := range []string{"linkd:lifecycle:recent-alert:ended", bkTenantID, eventID} {
		writeLengthPrefixed(digest, value)
	}
	return s.config.KeyPrefix + ":ended:" + strings.ToLower(keyEncoding.EncodeToString(digest.Sum(nil))), nil
}

func activeKey(alert domain.Alert) store.ActiveAlertKey {
	return store.ActiveAlertKey{
		BKTenantID: alert.BKTenantID, EventSourceID: alert.EventSourceID, Fingerprint: alert.Fingerprint,
	}
}

func recoverableTerminal(alert domain.Alert) bool {
	return alert.Status.Terminal() &&
		(alert.EndType == domain.AlertEndTypeSource || alert.EndType == domain.AlertEndTypeSeverityUpgrade)
}

func writeLengthPrefixed(destination hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write([]byte(value))
}
