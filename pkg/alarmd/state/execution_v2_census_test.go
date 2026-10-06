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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// censusBackend is casMemoryBackend with the one write it does not have.
// SetMany there is a no-op, which is enough for the paths that write through
// compare-and-set; a census is a plain replace, so a fake that dropped the
// write would let a test of what was stored pass against a store that stored
// nothing.
type censusBackend struct {
	*casMemoryBackend
	batches  [][]BackendWrite
	writeErr error
}

func newCensusBackend() *censusBackend {
	return &censusBackend{casMemoryBackend: &casMemoryBackend{values: make(map[string][]byte)}}
}

func (backend *censusBackend) SetMany(_ context.Context, writes []BackendWrite) error {
	backend.batches = append(backend.batches, append([]BackendWrite(nil), writes...))
	if backend.writeErr != nil {
		return backend.writeErr
	}
	if backend.writeTTLs == nil {
		backend.writeTTLs = make(map[string]time.Duration)
	}
	for _, write := range writes {
		backend.values[write.Key] = append([]byte(nil), write.Value...)
		backend.writeTTLs[write.Key] = write.TTL
	}
	return nil
}

func censusStore(t *testing.T, router StorageRouter, maxValueBytes int) *ExecutionStore {
	t.Helper()
	store, err := NewExecutionStore(ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: maxValueBytes, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func censusFixture(values int) execution.DimensionCensus {
	census := execution.DimensionCensus{
		Identity: execution.PlanCensusIdentity{
			Plan:            execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
			StateGeneration: "generation",
		},
		Source: execution.DimensionCensusFromRound, ObservedAt: 1000,
		Dimensions: []execution.DimensionCensusEntry{{Dimension: "ip"}},
	}
	for index := 0; index < values; index++ {
		census.Dimensions[0].Values = append(census.Dimensions[0].Values,
			execution.DimensionValueCount{Value: fmt.Sprintf("host-%05d", index), Series: 1})
		census.Series++
	}
	return census
}

// The census is written at Plan level, under its own kind, with no shard
// suffix: it describes the strategy's series, which is what a split is
// planned from, and a piece's view of them is not a different fact about the
// strategy.
func TestThePlanCensusKeyIsPlanLevelAndCarriesNoShard(t *testing.T) {
	identity := censusFixture(1).Identity
	key, err := PlanCensusKeyV2("alarmd", identity)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(key, ":")
	if len(parts) != 7 {
		t.Fatalf("key %q has %d segments, want the seven a Plan-level key has: a shard suffix would make eight, "+
			"and two shards of one strategy would then take a census of each other's pieces", key, len(parts))
	}
	if parts[1] != "census" {
		t.Fatalf("key %q is under kind %q, want its own kind: sharing one with the gap marker or the no-data "+
			"memory would make a census write land on a key another writer owns", key, parts[1])
	}
	gap, err := PlanGapKeyV2("alarmd", execution.PlanGapIdentity{Plan: identity.Plan, StateGeneration: identity.StateGeneration})
	if err != nil {
		t.Fatal(err)
	}
	if key == gap {
		t.Fatalf("the census and the gap marker share the key %q", key)
	}
}

// A census round-trips, and it is written at the lifetime floor: the next
// round that takes one renews it, so a strategy that stops being a candidate
// stops having a census instead of keeping a stale one for ever.
func TestACensusRoundTripsAtTheLifetimeFloorAndTouchesNoOtherKey(t *testing.T) {
	backend := newCensusBackend()
	store := censusStore(t, &fakeRouter{target: StorageTarget{Name: "state-01", Backend: backend}}, 512*1024)
	census := censusFixture(3)

	outcome, err := store.WriteCensus(context.Background(), census)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != execution.CensusWritten {
		t.Fatalf("WriteCensus() = %+v, want it stored", outcome)
	}
	if outcome.Bytes <= 0 || outcome.Bytes > outcome.Limit {
		t.Fatalf("WriteCensus() wrote %d bytes against a %d-byte cap, want a size inside it",
			outcome.Bytes, outcome.Limit)
	}

	key, err := PlanCensusKeyV2("alarmd", census.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.batches) != 1 || len(backend.batches[0]) != 1 {
		t.Fatalf("the census write made %d batches, want one holding one key: it must not touch anything else",
			len(backend.batches))
	}
	write := backend.batches[0][0]
	if write.Key != key {
		t.Fatalf("the census was written to %q, want %q", write.Key, key)
	}
	if write.TTL != store.options.MinTTL {
		t.Fatalf("the census was written with a %s lifetime, want the %s floor", write.TTL, store.options.MinTTL)
	}

	read, found, err := store.ReadCensus(context.Background(), census.Identity)
	if err != nil || !found {
		t.Fatalf("ReadCensus() = %v, %v, want the census back", found, err)
	}
	if read.Series != census.Series || len(read.Dimensions) != 1 || len(read.Dimensions[0].Values) != 3 {
		t.Fatalf("ReadCensus() = %+v, want what was written", read)
	}
	if read.Source != execution.DimensionCensusFromRound {
		t.Fatalf("ReadCensus() source = %q, want the source it was taken from: a reader that cannot tell a "+
			"round's census from the roster's cannot tell a distribution from an upper bound", read.Source)
	}
}

// Past the value cap the census is refused whole and named, and nothing is
// written. Truncating it to fit would leave a record that reads exactly like
// a strategy whose values really are those - the one failure a planner cannot
// detect from the record itself.
func TestACensusPastTheValueCapIsRefusedByNameAndNotWritten(t *testing.T) {
	backend := newCensusBackend()
	store := censusStore(t, &fakeRouter{target: StorageTarget{Name: "state-01", Backend: backend}}, 2048)
	census := censusFixture(400)

	outcome, err := store.WriteCensus(context.Background(), census)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != execution.CensusRejected {
		t.Fatalf("WriteCensus() = %+v, want it refused", outcome)
	}
	if string(outcome.ReasonCode) != contract.ReasonStateBudgetExceeded {
		t.Fatalf("WriteCensus() refused with %q, want %q: a refusal with no name is one nobody can count",
			outcome.ReasonCode, contract.ReasonStateBudgetExceeded)
	}
	if outcome.Bytes <= outcome.Limit {
		t.Fatalf("WriteCensus() reported %d bytes against a %d-byte cap, want the size that was too large: "+
			"the reading says how far over it was, not just that it was over", outcome.Bytes, outcome.Limit)
	}
	if len(backend.batches) != 0 {
		t.Fatalf("a refused census wrote %d batches, want none: a record cut to fit reads like a distribution",
			len(backend.batches))
	}
}

// A store that did not answer is not a census that was refused. The two are
// counted apart because one is this replica saying no and the other is the
// dependency being down, and only the second is worth waiting out.
func TestAStoreThatDidNotAnswerIsRetryableRatherThanRefused(t *testing.T) {
	backend := newCensusBackend()
	backend.writeErr = errors.New("connection refused")
	store := censusStore(t, &fakeRouter{target: StorageTarget{Name: "state-01", Backend: backend}}, 512*1024)

	outcome, err := store.WriteCensus(context.Background(), censusFixture(3))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != execution.CensusRetryable {
		t.Fatalf("WriteCensus() = %+v, want it retryable", outcome)
	}
	if string(outcome.ReasonCode) != contract.ReasonRedisUnavailable {
		t.Fatalf("WriteCensus() named %q, want %q", outcome.ReasonCode, contract.ReasonRedisUnavailable)
	}

	routed := censusStore(t, &fakeRouter{
		target: StorageTarget{Name: "state-01", Backend: newCensusBackend()},
		err:    errors.New("no target"),
	}, 512*1024)
	outcome, err = routed.WriteCensus(context.Background(), censusFixture(3))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != execution.CensusRetryable {
		t.Fatalf("WriteCensus() with no route = %+v, want it retryable", outcome)
	}
}

// A malformed census is refused before it is stored, so a reader never has to
// decide what a census with a value nothing carried means.
func TestAMalformedCensusIsRefusedBeforeItIsStored(t *testing.T) {
	backend := newCensusBackend()
	store := censusStore(t, &fakeRouter{target: StorageTarget{Name: "state-01", Backend: backend}}, 512*1024)
	census := censusFixture(3)
	census.Dimensions[0].Values[1].Series = 0

	outcome, err := store.WriteCensus(context.Background(), census)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != execution.CensusRejected || string(outcome.ReasonCode) != contract.ReasonStateCorrupt {
		t.Fatalf("WriteCensus() = %+v, want it refused as corrupt", outcome)
	}
	if len(backend.batches) != 0 {
		t.Fatalf("a malformed census was written in %d batches, want none", len(backend.batches))
	}
}

// A record this build cannot read is reported as no census rather than as an
// error: the planner's answer for "no census" is already "do not split",
// which is the safe reading of bytes it does not understand.
func TestACensusRecordThisBuildCannotReadIsReportedAsAbsent(t *testing.T) {
	backend := newCensusBackend()
	store := censusStore(t, &fakeRouter{target: StorageTarget{Name: "state-01", Backend: backend}}, 512*1024)
	identity := censusFixture(1).Identity
	key, err := PlanCensusKeyV2("alarmd", identity)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := json.Marshal(map[string]any{"schema": "dimension-census-v2", "census": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	backend.values[key] = newer

	read, found, err := store.ReadCensus(context.Background(), identity)
	if err != nil {
		t.Fatalf("ReadCensus() error = %v, want a newer record reported as absent rather than as a failure", err)
	}
	if found {
		t.Fatalf("ReadCensus() = %+v, want no census: this build cannot read that record", read)
	}
}
