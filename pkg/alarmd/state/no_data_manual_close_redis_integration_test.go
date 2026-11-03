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
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

func manualCloseTestStore(t *testing.T) (*ExecutionStore, *RedisBackend, *redis.Client) {
	t.Helper()
	executable := redistest.Server(t)
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	backend, err := NewRedisBackend(RedisBackendOptions{
		Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	waitRedisReady(t, backend)
	direct := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = direct.Close() })
	router, err := NewFixedRouter("primary", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewExecutionStore(ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: 1 << 20, MaxItemsPerCall: 64,
		MinTTL: time.Minute, MaxTTL: 48 * time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, backend, direct
}

const (
	closedFingerprintA = "0123456789abcdef0123456789abcdef"
	closedFingerprintB = "fedcba9876543210fedcba9876543210"
)

// A close is recorded with the time alarmd received it, a repeat only moves
// that time, the key lives the memory's floor, and a read names only the
// fingerprints that were closed.
func TestAManualCloseIsRecordedAndReadBack(t *testing.T) {
	store, _, direct := manualCloseTestStore(t)
	ctx := context.Background()
	first, second := time.Unix(1_790_000_000, 0), time.Unix(1_790_000_600, 0)
	if err := store.RecordNoDataManualClose(ctx, "system", "7", closedFingerprintA, first); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordNoDataManualClose(ctx, "system", "7", closedFingerprintA, second); err != nil {
		t.Fatal(err)
	}
	closes, err := store.ReadNoDataManualCloses(ctx, "system", "7", []string{closedFingerprintA, closedFingerprintB})
	if err != nil || len(closes) != 1 || closes[closedFingerprintA] != second.Unix() {
		t.Fatalf("closes = %+v, err = %v; want only A, at the repeat's time", closes, err)
	}
	key, _ := NoDataManualCloseKey("alarmd", "system", "7")
	ttl, err := direct.PTTL(ctx, key).Result()
	if err != nil || ttl <= NoDataManualCloseTTL-time.Minute || ttl > NoDataManualCloseTTL {
		t.Fatalf("TTL = %v, err = %v; want the no-data memory floor", ttl, err)
	}
	if other, err := store.ReadNoDataManualCloses(ctx, "system", "8", []string{closedFingerprintA}); err != nil || len(other) != 0 {
		t.Fatalf("another strategy read %+v, %v", other, err)
	}
}

// The pipelined read hands back the memory and the closes in one round trip,
// the closes parsed and anything that is not a close left out.
func TestTheMemoryAndTheClosesAreReadTogether(t *testing.T) {
	_, backend, direct := manualCloseTestStore(t)
	ctx := context.Background()
	if err := direct.HSet(ctx, "memory", "_meta", "header").Err(); err != nil {
		t.Fatal(err)
	}
	if err := direct.HSet(ctx, "closes", closedFingerprintA, "1790000000", "junk", "x").Err(); err != nil {
		t.Fatal(err)
	}
	memory, closes, closesErr, err := backend.ReadHashPair(ctx, "memory", "closes")
	if err != nil || closesErr != nil || string(memory["_meta"]) != "header" {
		t.Fatalf("memory = %+v, err = %v, closes err = %v", memory, err, closesErr)
	}
	parsed := parseNoDataManualCloses(closes)
	if len(parsed) != 1 || parsed[closedFingerprintA] != 1_790_000_000 {
		t.Fatalf("closes = %+v, want the one valid close", parsed)
	}
}

// A close key that cannot be read as a hash costs the closes alone: the
// memory beside it in the pipeline still comes back.
func TestACloseKeyThatCannotBeReadDoesNotCostTheMemory(t *testing.T) {
	_, backend, direct := manualCloseTestStore(t)
	ctx := context.Background()
	if err := direct.HSet(ctx, "memory", "_meta", "header").Err(); err != nil {
		t.Fatal(err)
	}
	if err := direct.Set(ctx, "closes", "not a hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	memory, closes, closesErr, err := backend.ReadHashPair(ctx, "memory", "closes")
	if err != nil || string(memory["_meta"]) != "header" {
		t.Fatalf("memory = %+v, err = %v; want the memory read", memory, err)
	}
	if closesErr == nil || closes != nil {
		t.Fatalf("closes = %+v, closes err = %v; want the close read's own failure", closes, closesErr)
	}
}

// One strategy holds at most MaxNoDataManualClosesPerStrategy closes; a
// repeat of one already held is still accepted at the bound.
func TestAStrategysClosesAreBounded(t *testing.T) {
	_, backend, _ := manualCloseTestStore(t)
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		set, err := backend.RecordHashField(ctx, "closes", strconv.Itoa(index), []byte("1"), time.Minute, 3)
		if err != nil || !set {
			t.Fatalf("close %d = %t, %v", index, set, err)
		}
	}
	if set, err := backend.RecordHashField(ctx, "closes", "3", []byte("1"), time.Minute, 3); err != nil || set {
		t.Fatalf("a close past the bound = %t, %v; want refused", set, err)
	}
	if set, err := backend.RecordHashField(ctx, "closes", "0", []byte("2"), time.Minute, 3); err != nil || !set {
		t.Fatalf("a repeat at the bound = %t, %v; want accepted", set, err)
	}
}

func TestAManualCloseNeedsAFingerprint(t *testing.T) {
	store, _, _ := manualCloseTestStore(t)
	err := store.RecordNoDataManualClose(context.Background(), "system", "7", "not-a-fingerprint", time.Now())
	if err == nil || errors.Is(err, ErrNoDataManualClosesFull) {
		t.Fatalf("an invalid fingerprint was recorded: %v", err)
	}
}

// A strategy whose close key cannot be built has no closes, at the output
// check as at the memory load: nothing to check, not a failed check.
func TestAnUnbuildableCloseKeyIsNoClosesAtBothReads(t *testing.T) {
	store, _, _ := manualCloseTestStore(t)
	closes, err := store.ReadNoDataManualCloses(context.Background(), "system", "not-a-strategy", []string{closedFingerprintA})
	if err != nil || len(closes) != 0 {
		t.Fatalf("closes = %+v, err = %v; want none and no error", closes, err)
	}
}
