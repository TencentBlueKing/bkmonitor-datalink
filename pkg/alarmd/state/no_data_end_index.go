// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// NoDataEndBackend keeps the no-data end index (execution.NoDataEndIndex):
// one hash per Plan, read whole a page at a time and written field by field.
// No header and no version: an entry is the newest word about one series'
// open alert, a second owner's write during a handover says the same thing,
// and a lost write is made again by the next round that evaluates the
// series.
type NoDataEndBackend interface {
	Backend
	ScanHash(ctx context.Context, key string, count int64) (map[string][]byte, error)
	WriteHashFields(ctx context.Context, key string, puts map[string][]byte, deletes []string, ttl time.Duration) error
}

// noDataEndScanCount is how many fields one HSCAN of an index asks for. An
// entry is a field of about 66 bytes and a value of 110 to 300 (the alert's
// dimensions), so a page is a few hundred kilobytes: a Plan at its cap of
// MaxNoDataEndEntriesPerPlan entries is read in about a hundred commands,
// none of which holds the server for long, where one HGETALL would return
// 20 to 40 MB in one. Values over 64 bytes keep the hash in its hashtable
// encoding, so the count is honoured rather than the whole hash returned.
const noDataEndScanCount = 1000

// ScanHash reads one hash whole with HSCAN, count fields a command. A field
// HSCAN returns twice is kept once; one written during the scan may be
// missed, which costs that entry this round and nothing after.
func (backend *RedisBackend) ScanHash(ctx context.Context, key string, count int64) (map[string][]byte, error) {
	if backend == nil || backend.client == nil {
		return nil, fmt.Errorf("state: redis backend is not configured")
	}
	if key == "" || count <= 0 {
		return nil, fmt.Errorf("state: invalid hash scan")
	}
	var record map[string][]byte
	for cursor := uint64(0); ; {
		page, next, err := backend.client.HScan(ctx, key, cursor, "", count).Result()
		if err != nil {
			return nil, err
		}
		for index := 0; index+1 < len(page); index += 2 {
			if record == nil {
				record = make(map[string][]byte, len(page)/2)
			}
			record[page[index]] = []byte(page[index+1])
		}
		if next == 0 {
			return record, nil
		}
		cursor = next
	}
}

// WriteHashFields puts and deletes fields of one hash and sets its life, in
// one round trip.
func (backend *RedisBackend) WriteHashFields(ctx context.Context, key string, puts map[string][]byte, deletes []string, ttl time.Duration) error {
	if backend == nil || backend.client == nil {
		return fmt.Errorf("state: redis backend is not configured")
	}
	if key == "" || ttl <= 0 {
		return fmt.Errorf("state: invalid hash field write")
	}
	if len(puts) == 0 && len(deletes) == 0 {
		return nil
	}
	_, err := backend.client.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
		if len(puts) > 0 {
			values := make([]interface{}, 0, 2*len(puts))
			for field, value := range puts {
				values = append(values, field, value)
			}
			pipeline.HSet(ctx, key, values...)
		}
		if len(deletes) > 0 {
			pipeline.HDel(ctx, key, deletes...)
		}
		pipeline.Expire(ctx, key, ttl)
		return nil
	})
	return err
}

// NoDataEndKeyV2 names one Plan's no-data end index under its generation.
func NoDataEndKeyV2(prefix string, key execution.NoDataEndKey) (string, error) {
	if err := validatePlanIdentity(prefix, key.Plan, key.StateGeneration); err != nil {
		return "", err
	}
	return executionKey(prefix, "nodata-end", key.Plan, key.StateGeneration, ""), nil
}

// noDataEndField is an entry's field: the series and the level.
func noDataEndField(key execution.NoDataEndEntryKey) string {
	return string(key.Series) + ":" + strconv.FormatUint(uint64(key.LevelID), 10)
}

// noDataEndValue is an entry's value, the alert's fingerprint, dimensions and
// the last real point's source time, and a global business Plan's attributed
// business, omitted on every other Plan's entries. Read with a plain decode,
// so a build that does not know a field passes over it.
type noDataEndValue struct {
	Fingerprint          string                     `json:"f"`
	Dimensions           map[string]json.RawMessage `json:"d"`
	LastRealPoint        int64                      `json:"t"`
	AttributedBusinessID string                     `json:"b,omitempty"`
}

func decodeNoDataEndEntry(field string, value []byte) (execution.NoDataEndEntry, bool) {
	series, level, found := strings.Cut(field, ":")
	if !found || series == "" {
		return execution.NoDataEndEntry{}, false
	}
	levelID, err := strconv.ParseUint(level, 10, 32)
	if err != nil || levelID == 0 {
		return execution.NoDataEndEntry{}, false
	}
	var decoded noDataEndValue
	if err := json.Unmarshal(value, &decoded); err != nil || decoded.Fingerprint == "" || decoded.LastRealPoint <= 0 {
		return execution.NoDataEndEntry{}, false
	}
	return execution.NoDataEndEntry{Series: execution.SeriesIdentityDigest(series), LevelID: uint32(levelID),
		Fingerprint: decoded.Fingerprint, Dimensions: decoded.Dimensions, LastRealPoint: decoded.LastRealPoint,
		AttributedBusinessID: decoded.AttributedBusinessID}, true
}

// LoadNoDataEnds reads each Plan's index. A Plan whose index cannot be read
// is Unavailable, not empty.
func (store *ExecutionStore) LoadNoDataEnds(ctx context.Context, keys []execution.NoDataEndKey) ([]execution.NoDataEndLoaded, error) {
	loaded := make([]execution.NoDataEndLoaded, 0, len(keys))
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := execution.NoDataEndLoaded{Key: key}
		backend, redisKey, err := store.noDataEndTarget(key)
		if err != nil {
			item.Unavailable = true
			loaded = append(loaded, item)
			continue
		}
		fields, err := backend.ScanHash(ctx, redisKey, noDataEndScanCount)
		if err != nil {
			item.Unavailable = true
			loaded = append(loaded, item)
			continue
		}
		for field, value := range fields {
			entry, ok := decodeNoDataEndEntry(field, value)
			if !ok {
				item.Undecodable++
				continue
			}
			item.Entries = append(item.Entries, entry)
		}
		loaded = append(loaded, item)
	}
	return loaded, nil
}

// ApplyNoDataEnds writes one Plan's changes to its index.
func (store *ExecutionStore) ApplyNoDataEnds(ctx context.Context, write execution.NoDataEndWrite) error {
	backend, redisKey, err := store.noDataEndTarget(write.Key)
	if err != nil {
		return err
	}
	puts := make(map[string][]byte, len(write.Puts))
	for _, entry := range write.Puts {
		value, err := json.Marshal(noDataEndValue{Fingerprint: entry.Fingerprint, Dimensions: entry.Dimensions, LastRealPoint: entry.LastRealPoint,
			AttributedBusinessID: entry.AttributedBusinessID})
		if err != nil {
			return fmt.Errorf("state: encode no-data end entry: %w", err)
		}
		puts[noDataEndField(entry.Key())] = value
	}
	deletes := make([]string, 0, len(write.Deletes))
	for _, key := range write.Deletes {
		deletes = append(deletes, noDataEndField(key))
	}
	return backend.WriteHashFields(ctx, redisKey, puts, deletes, write.TTL)
}

func (store *ExecutionStore) noDataEndTarget(key execution.NoDataEndKey) (NoDataEndBackend, string, error) {
	target, err := store.options.Router.Route(key.Plan.TenantID, key.Plan.StrategyID)
	if err != nil {
		return nil, "", err
	}
	backend, ok := target.Backend.(NoDataEndBackend)
	if !ok {
		return nil, "", fmt.Errorf("state: the backend for strategy %s keeps no no-data end index", key.Plan.StrategyID)
	}
	redisKey, err := NoDataEndKeyV2(store.options.Prefix, key)
	if err != nil {
		return nil, "", err
	}
	return backend, redisKey, nil
}

var _ execution.NoDataEndIndex = (*ExecutionStore)(nil)
