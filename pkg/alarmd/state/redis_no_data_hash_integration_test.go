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
	"os/exec"
	"testing"
	"time"
)

// What the script does with a replacing write, executed by Redis rather than
// restated in Go.
//
// The in-memory backend the other tests use models this branch, which means
// those tests pass whether or not the script has it: a script that applied a
// whole-memory statement on top of what the record held would leave them all
// green while every migrated Plan kept groups its memory no longer has. This
// is the only test that can tell, and it is the same reason the renewal script
// has one.
func TestApplyHashDeltaReplacesTheWholeRecordWhenAsked(t *testing.T) {
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	backend, err := NewRedisBackend(RedisBackendOptions{
		Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
	})
	if err != nil {
		t.Fatalf("NewRedisBackend() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	waitRedisReady(t, backend)
	ctx := context.Background()

	const key = "record"
	first := HashDeltaWrite{
		Key: key, HeaderField: "_meta", ExpectedMissing: true, Header: []byte("header-1"),
		Set: []HashField{{Name: "g:kept", Value: []byte("p")}, {Name: "g:stale", Value: []byte("p")}},
		TTL: time.Minute,
	}
	outcome, err := backend.ApplyHashDelta(ctx, first)
	if err != nil || outcome.Status != HashDeltaApplied {
		t.Fatalf("first ApplyHashDelta() = (%+v, %v)", outcome, err)
	}

	// A delta leaves what it does not mention.
	delta := HashDeltaWrite{
		Key: key, HeaderField: "_meta", ExpectedDigest: HeaderDigest([]byte("header-1")),
		Header: []byte("header-2"),
		Set:    []HashField{{Name: "g:kept", Value: []byte("q")}},
		TTL:    time.Minute,
	}
	if outcome, err = backend.ApplyHashDelta(ctx, delta); err != nil || outcome.Status != HashDeltaApplied {
		t.Fatalf("delta ApplyHashDelta() = (%+v, %v)", outcome, err)
	}
	record, err := backend.ReadHash(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := record["g:stale"]; !kept {
		t.Fatal("a delta removed a field it did not mention; that is the whole difference between the two")
	}

	// A replace does not.
	replace := HashDeltaWrite{
		Key: key, HeaderField: "_meta", ExpectedDigest: HeaderDigest([]byte("header-2")),
		Header:  []byte("header-3"),
		Set:     []HashField{{Name: "g:kept", Value: []byte("p")}},
		Replace: true, TTL: time.Minute,
	}
	if outcome, err = backend.ApplyHashDelta(ctx, replace); err != nil || outcome.Status != HashDeltaApplied {
		t.Fatalf("replace ApplyHashDelta() = (%+v, %v)", outcome, err)
	}
	record, err = backend.ReadHash(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, left := record["g:stale"]; left {
		t.Fatalf("a replacing write left %v behind. The statement carries the whole memory, so a field "+
			"it does not mention is a group the memory no longer has", record)
	}
	if len(record) != 2 || string(record["_meta"]) != "header-3" || string(record["g:kept"]) != "p" {
		t.Fatalf("record = %v, want the header and the one group the statement carried", record)
	}
	// The delete and the writes are one script, so the record is never
	// observably empty -- and the key keeps a lifetime rather than being
	// recreated without one.
	remaining := remainingLife(t, backend, key)
	if remaining <= 0 || remaining > time.Minute {
		t.Fatalf("remaining life = %s, want the write's own minute; a replaced key that lost its "+
			"expiry outlives the generation it belongs to", remaining)
	}
}

// A replacing write still proves it read the record it is replacing.
//
// Relaxing the revision comparison for statements derived from another record
// must not relax the race guard: two writers replacing the same record would
// otherwise each overwrite the other, and the loser would never know.
func TestAReplacingWriteStillLosesToAConcurrentOne(t *testing.T) {
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	backend, err := NewRedisBackend(RedisBackendOptions{
		Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
	})
	if err != nil {
		t.Fatalf("NewRedisBackend() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	waitRedisReady(t, backend)
	ctx := context.Background()

	const key = "contended"
	if outcome, err := backend.ApplyHashDelta(ctx, HashDeltaWrite{
		Key: key, HeaderField: "_meta", ExpectedMissing: true, Header: []byte("header-1"),
		Set: []HashField{{Name: "g:a", Value: []byte("p")}}, TTL: time.Minute,
	}); err != nil || outcome.Status != HashDeltaApplied {
		t.Fatalf("seed ApplyHashDelta() = (%+v, %v)", outcome, err)
	}
	// Somebody else wrote between this writer's read and its write.
	if outcome, err := backend.ApplyHashDelta(ctx, HashDeltaWrite{
		Key: key, HeaderField: "_meta", ExpectedDigest: HeaderDigest([]byte("header-1")),
		Header: []byte("header-2"), Set: []HashField{{Name: "g:b", Value: []byte("p")}}, TTL: time.Minute,
	}); err != nil || outcome.Status != HashDeltaApplied {
		t.Fatalf("racing ApplyHashDelta() = (%+v, %v)", outcome, err)
	}
	outcome, err := backend.ApplyHashDelta(ctx, HashDeltaWrite{
		Key: key, HeaderField: "_meta", ExpectedDigest: HeaderDigest([]byte("header-1")),
		Header: []byte("header-3"), Set: []HashField{{Name: "g:c", Value: []byte("p")}},
		Replace: true, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != HashDeltaConflict {
		t.Fatalf("replace = %q, want a conflict: a replacing write that skipped the guard would "+
			"silently discard the round that got in first", outcome.Status)
	}
	if string(outcome.Current) != "header-2" {
		t.Fatalf("conflict carried %q, want the header Redis holds so the caller can classify it",
			outcome.Current)
	}
	record, err := backend.ReadHash(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, survived := record["g:b"]; !survived {
		t.Fatalf("the refused replace deleted the winner's field anyway: %v", record)
	}
}
