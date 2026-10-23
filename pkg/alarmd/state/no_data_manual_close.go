// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// The manual-close intent store (decision-024): one hash per (tenant,
// strategy), field = the alert fingerprint a user closed, value = the unix
// second alarmd recorded the close. A close stops the absence that was open
// when it was recorded (nodata.ManualCloses), so nothing consumes or deletes
// a field: the key lives the life of the no-data memory it acts on and is
// renewed by every write.
const (
	// NoDataManualCloseTTL is the life of a strategy's close hash: the floor
	// a generation-scoped no-data memory lives (GenerationScopedFloor). Past
	// it the memory a close would act on has expired too.
	NoDataManualCloseTTL = GenerationScopedFloor
	// MaxNoDataManualClosesPerStrategy bounds one strategy's hash. Manual
	// closes are orders of magnitude fewer; the bound is what keeps one
	// HGETALL per no-data Plan per Slot small whatever a caller does.
	MaxNoDataManualClosesPerStrategy = 10000
)

// ErrNoDataManualClosesFull is a close refused because its strategy's hash
// holds MaxNoDataManualClosesPerStrategy already.
var ErrNoDataManualClosesFull = errors.New("state: no-data manual close store is full for this strategy")

var noDataFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidNoDataFingerprint is whether value can be an alert fingerprint.
func ValidNoDataFingerprint(value string) bool { return noDataFingerprintPattern.MatchString(value) }

// NoDataManualCloseKey names one strategy's close hash. The tenant is
// digested as every execution key's tenant is, so a tenant id never reaches a
// key name.
func NoDataManualCloseKey(prefix, tenantID, strategyID string) (string, error) {
	if strings.TrimSpace(prefix) == "" || strings.ContainsAny(prefix, "{} \t\r\n") {
		return "", fmt.Errorf("state: invalid key prefix")
	}
	if strings.TrimSpace(tenantID) == "" {
		return "", fmt.Errorf("state: manual close needs a tenant")
	}
	if strategy, err := strconv.ParseInt(strategyID, 10, 64); err != nil || strategy <= 0 || strconv.FormatInt(strategy, 10) != strategyID {
		return "", fmt.Errorf("state: manual close needs a positive strategy id")
	}
	tenant := tenantKeySegments.digest(tenantID)
	return strings.Join([]string{prefix, "nodata-close", "v1", tenant[:32], strategyID}, ":"), nil
}

// NoDataManualCloseBackend is the capability the close store needs of a
// backend. It is optional and not probed at open: a backend without it holds
// no closes, so the no-data load reads the memory alone and a record is
// refused, which the route answers as store_unavailable.
type NoDataManualCloseBackend interface {
	// ReadHashPair reads two whole hashes in one round trip. err is the
	// first's failure, or the round trip's; secondErr is the second's alone,
	// so a second hash that cannot be read never costs the first.
	ReadHashPair(ctx context.Context, first, second string) (firstFields, secondFields map[string][]byte, secondErr, err error)
	// ReadHashFields reads some fields of one hash; a missing field is nil.
	ReadHashFields(ctx context.Context, key string, fields []string) ([][]byte, error)
	// RecordHashField sets one field unless the hash already holds max other
	// fields, and renews the key's life.
	RecordHashField(ctx context.Context, key, field string, value []byte, ttl time.Duration, max int) (bool, error)
}

// parseNoDataManualCloses turns a close hash into fingerprint → close time.
// A field that is not a fingerprint or a value that is not a positive second
// is skipped: it names nothing this build could match.
func parseNoDataManualCloses(fields map[string][]byte) map[string]int64 {
	if len(fields) == 0 {
		return nil
	}
	closes := make(map[string]int64, len(fields))
	for fingerprint, raw := range fields {
		at, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil || at <= 0 || !ValidNoDataFingerprint(fingerprint) {
			continue
		}
		closes[fingerprint] = at
	}
	return closes
}

// RecordNoDataManualClose records that a user closed the no-data alert named
// by fingerprint, at the time alarmd received it. Recording it again only
// moves that time.
func (store *ExecutionStore) RecordNoDataManualClose(
	ctx context.Context, tenantID, strategyID, fingerprint string, at time.Time,
) error {
	if !ValidNoDataFingerprint(fingerprint) {
		return fmt.Errorf("state: manual close needs a 32 hex fingerprint")
	}
	key, err := NoDataManualCloseKey(store.options.Prefix, tenantID, strategyID)
	if err != nil {
		return err
	}
	target, err := store.options.Router.Route(tenantID, strategyID)
	if err != nil {
		return err
	}
	backend, ok := target.Backend.(NoDataManualCloseBackend)
	if !ok {
		return ErrLifetimeUnsupported
	}
	recorded, err := backend.RecordHashField(ctx, key, fingerprint,
		[]byte(strconv.FormatInt(at.Unix(), 10)), NoDataManualCloseTTL, MaxNoDataManualClosesPerStrategy)
	if err != nil {
		return err
	}
	if !recorded {
		return ErrNoDataManualClosesFull
	}
	return nil
}

// ReadNoDataManualCloses reads, for the fingerprints given, the close time a
// strategy's hash holds; a fingerprint with no close is absent from the map.
func (store *ExecutionStore) ReadNoDataManualCloses(
	ctx context.Context, tenantID, strategyID string, fingerprints []string,
) (map[string]int64, error) {
	if len(fingerprints) == 0 {
		return nil, nil
	}
	key, err := NoDataManualCloseKey(store.options.Prefix, tenantID, strategyID)
	if err != nil {
		// A key that cannot be built is one no close was ever recorded under,
		// as the memory load reads it: nothing to check, not a failed check.
		return nil, nil
	}
	target, err := store.options.Router.Route(tenantID, strategyID)
	if err != nil {
		return nil, err
	}
	backend, ok := target.Backend.(NoDataManualCloseBackend)
	if !ok {
		return nil, nil
	}
	values, err := backend.ReadHashFields(ctx, key, fingerprints)
	if err != nil {
		return nil, err
	}
	fields := make(map[string][]byte, len(fingerprints))
	for index, value := range values {
		if value != nil && index < len(fingerprints) {
			fields[fingerprints[index]] = value
		}
	}
	return parseNoDataManualCloses(fields), nil
}

// recordHashFieldScript sets one field unless the hash already holds max
// other fields, then renews the key. KEYS[1] the hash; ARGV field, value,
// ttl in milliseconds, max. Returns 1 when set, 0 when full.
const recordHashFieldScript = `
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 0 and redis.call('HLEN', KEYS[1]) >= tonumber(ARGV[4]) then
  return 0
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`

var recordHashFieldSHA = func() string {
	sum := sha1.Sum([]byte(recordHashFieldScript))
	return hex.EncodeToString(sum[:])
}()

func (backend *RedisBackend) ReadHashPair(
	ctx context.Context, first, second string,
) (map[string][]byte, map[string][]byte, error, error) {
	if backend == nil || backend.client == nil {
		return nil, nil, nil, fmt.Errorf("state: redis backend is not configured")
	}
	var firstCmd, secondCmd *redis.StringStringMapCmd
	// The pipeline's own error is the first command that failed, whichever
	// it was; each command's is read instead.
	_, _ = backend.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		firstCmd = pipe.HGetAll(ctx, first)
		secondCmd = pipe.HGetAll(ctx, second)
		return nil
	})
	if err := firstCmd.Err(); err != nil {
		return nil, nil, nil, err
	}
	if err := secondCmd.Err(); err != nil {
		return hashBytes(firstCmd.Val()), nil, err, nil
	}
	return hashBytes(firstCmd.Val()), hashBytes(secondCmd.Val()), nil, nil
}

func hashBytes(fields map[string]string) map[string][]byte {
	if len(fields) == 0 {
		return nil
	}
	record := make(map[string][]byte, len(fields))
	for name, value := range fields {
		record[name] = []byte(value)
	}
	return record
}

func (backend *RedisBackend) ReadHashFields(ctx context.Context, key string, fields []string) ([][]byte, error) {
	if backend == nil || backend.client == nil {
		return nil, fmt.Errorf("state: redis backend is not configured")
	}
	var cmd *redis.SliceCmd
	if _, err := backend.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		cmd = pipe.HMGet(ctx, key, fields...)
		return nil
	}); err != nil {
		return nil, err
	}
	values := make([][]byte, len(cmd.Val()))
	for index, value := range cmd.Val() {
		if text, ok := value.(string); ok {
			values[index] = []byte(text)
		}
	}
	return values, nil
}

func (backend *RedisBackend) RecordHashField(
	ctx context.Context, key, field string, value []byte, ttl time.Duration, max int,
) (bool, error) {
	if backend == nil || backend.client == nil {
		return false, fmt.Errorf("state: redis backend is not configured")
	}
	keys := []string{key}
	args := []interface{}{field, value, ttl.Milliseconds(), max}
	reply, err := backend.client.EvalSha(ctx, recordHashFieldSHA, keys, args...).Result()
	if noScriptReply(err) {
		reply, err = backend.client.Eval(ctx, recordHashFieldScript, keys, args...).Result()
	}
	if err != nil {
		return false, err
	}
	set, ok := reply.(int64)
	if !ok {
		return false, fmt.Errorf("state: Redis record reply %T is not an integer", reply)
	}
	return set == 1, nil
}

var _ NoDataManualCloseBackend = (*RedisBackend)(nil)
