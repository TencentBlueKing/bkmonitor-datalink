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
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// frozenRenewalFixture is a store with one series already written by the apply
// path, and the key and lifetime that write produced.
//
// The write is real rather than a hand-placed value, because the two facts
// every test below compares against -- which key holds this series, and how
// long it was given to live -- are the write path's answers. Restating either
// of them here would make these tests agree with the renewal about a key that
// no write ever touches, which is a renewal that keeps the wrong key alive and
// passes every test.
func frozenRenewalFixture(t *testing.T) (*ExecutionStore, *casMemoryBackend, string, time.Duration) {
	t.Helper()
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, err := NewFixedRouter("target", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewExecutionStore(ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: stateIdentityV2(), ApplyVersion: applyVersion(),
		AffectedRecords: []execution.RecordAnchor{derivedAnchor(t, stateIdentityV2(), 60)},
		Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat",
			HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}},
		Points: []execution.StateHistoryPoint{derivedPoint(t, stateIdentityV2(), 60, "detect", execution.LevelFactNormal)},
	})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: []execution.StateMutation{mutation},
	})
	if err != nil || applied.Items[0].Status != execution.StateApplied {
		t.Fatalf("ApplyRuntime() = (%+v, %v), want the series written", applied, err)
	}
	if len(backend.writeTTLs) != 1 {
		t.Fatalf("the apply wrote %d keys, want exactly one to compare the renewal against", len(backend.writeTTLs))
	}
	var key string
	var ttl time.Duration
	for written, lifetime := range backend.writeTTLs {
		key, ttl = written, lifetime
	}
	if ttl <= 0 {
		t.Fatal("the apply wrote a key with no lifetime; there is nothing for a renewal to match")
	}
	backend.renewals = nil
	return store, backend, key, ttl
}

func frozenRequest(now time.Time) execution.FrozenStateRenewalRequest {
	return execution.FrozenStateRenewalRequest{
		Contract: frozenRef(), Retention: testRetention(), Now: now,
		Items: []execution.FrozenSeriesState{{
			Identity: stateIdentityV2(), LastApplied: applyVersion().EvaluationTime,
			Representation: execution.StateRepresentationFramed,
		}},
	}
}

// writtenAt is the instant the fixture's write happened, as the blob records
// it. Every age below is stated as an offset from here.
func writtenAt() time.Time { return time.Unix(int64(applyVersion().EvaluationTime), 0) }

// A frozen key is asked about only once it is actually running out.
//
// The gate exists because the alternative was measured and rejected: asking on
// every read doubles the command rate of the whole read path to keep alive the
// one percent of keys that are frozen. The remaining life is already known
// from the blob -- the write that stored it set the TTL -- so below half a life
// there is nothing to ask.
func TestAFrozenKeyIsAskedAboutOnlyOnceItIsRunningOut(t *testing.T) {
	for _, test := range []struct {
		name    string
		ageOf   func(ttl time.Duration) time.Duration
		wantAsk bool
	}{
		{name: "just written", ageOf: func(time.Duration) time.Duration { return time.Second }},
		{name: "a moment before half its life", ageOf: func(ttl time.Duration) time.Duration { return ttl/2 - time.Second }},
		{name: "exactly half its life", ageOf: func(ttl time.Duration) time.Duration { return ttl / 2 }, wantAsk: true},
		{name: "well past half its life", ageOf: func(ttl time.Duration) time.Duration { return ttl - time.Second }, wantAsk: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, backend, key, ttl := frozenRenewalFixture(t)
			age := test.ageOf(ttl)
			result, err := store.RenewFrozenRuntime(context.Background(), frozenRequest(writtenAt().Add(age)))
			if err != nil {
				t.Fatalf("RenewFrozenRuntime() error = %v", err)
			}
			if len(result.Items) != 1 {
				t.Fatalf("result = %+v, want one item per requested series", result.Items)
			}
			if !test.wantAsk {
				if len(backend.renewals) != 0 {
					t.Fatalf("a key %s old with a %s life was asked about: %+v. The read path pays for "+
						"every ask, and this one had nothing to decide", age, ttl, backend.renewals)
				}
				if result.Items[0].Outcome != execution.FrozenRenewalFresh {
					t.Fatalf("outcome = %q, want %q for a key decided without asking",
						result.Items[0].Outcome, execution.FrozenRenewalFresh)
				}
				return
			}
			if len(backend.renewals) != 1 {
				t.Fatalf("renewals = %+v, want exactly one for a key %s into a %s life", backend.renewals, age, ttl)
			}
			call := backend.renewals[0]
			if call.Key != key {
				t.Fatalf("renewed %q, want the key the write path wrote (%q). A renewal on a key nothing "+
					"writes keeps the wrong record alive and lets the real one expire", call.Key, key)
			}
			if call.TTL != ttl {
				t.Fatalf("renewed with a %s life, want the %s the write gave it. A life derived twice is "+
					"two lives, and the shorter one expires a window that is still in use", call.TTL, ttl)
			}
			if call.Threshold != ttl/2 {
				t.Fatalf("threshold = %s, want half the life (%s)", call.Threshold, ttl/2)
			}
			if result.Items[0].Outcome != execution.FrozenRenewalRenewed {
				t.Fatalf("outcome = %q, want %q", result.Items[0].Outcome, execution.FrozenRenewalRenewed)
			}
		})
	}
}

// A key that vanished says so, and does not read as a healthy one.
//
// This is the whole reason the reply gained a third value. The event it names
// -- state deleted while its Plan was still evaluating the series every minute
// -- used to be invisible unless the expiry happened to land in the few seconds
// between a Slot's read and its write, and then it arrived as a version
// conflict that said nothing about why.
func TestAFrozenKeyThatVanishedIsNamedRatherThanCountedAsHealthy(t *testing.T) {
	store, backend, key, ttl := frozenRenewalFixture(t)
	delete(backend.values, key)
	result, err := store.RenewFrozenRuntime(context.Background(),
		frozenRequest(writtenAt().Add(ttl-time.Second)))
	if err != nil {
		t.Fatalf("RenewFrozenRuntime() error = %v", err)
	}
	if result.Items[0].Outcome != execution.FrozenRenewalMissing {
		t.Fatalf("outcome = %q, want %q. A series whose state was deleted must not be counted beside the "+
			"ones that are fine", result.Items[0].Outcome, execution.FrozenRenewalMissing)
	}
	if len(backend.values) != 0 {
		t.Fatalf("the renewal recreated %d keys; a key with no value reads as corrupt state to every "+
			"reader", len(backend.values))
	}
}

// A failure is that series' own outcome, named, and nothing else.
func TestAFailedRenewalIsCountedAndNamed(t *testing.T) {
	store, backend, _, ttl := frozenRenewalFixture(t)
	backend.renewalErr = errors.New("redis is unreachable")
	result, err := store.RenewFrozenRuntime(context.Background(),
		frozenRequest(writtenAt().Add(ttl-time.Second)))
	if err != nil {
		t.Fatalf("RenewFrozenRuntime() error = %v, want the failure carried per series rather than "+
			"returned; a Slot must not fail because a key life could not be extended", err)
	}
	if result.Items[0].Outcome != execution.FrozenRenewalFailed {
		t.Fatalf("outcome = %q, want %q", result.Items[0].Outcome, execution.FrozenRenewalFailed)
	}
	if result.Items[0].ReasonCode == "" {
		t.Fatal("the failure has no reason; a failure without a name is not something anyone can act on")
	}
}

// A key just asked about is not asked about again next Slot.
//
// Without this the mechanism costs what it was designed not to cost. Age is
// measured from the last write, and a renewal is not a write, so once a frozen
// key crosses half its life the age gate says yes on every Slot for as long as
// the freeze lasts -- which is exactly the population that freezes for hours.
// The ask that just returned is what pays for the skip: it left the key with at
// least the threshold remaining, and the gate spends half of that.
func TestAKeyJustRenewedIsNotAskedAboutOnTheNextSlot(t *testing.T) {
	store, backend, _, ttl := frozenRenewalFixture(t)
	first, err := store.RenewFrozenRuntime(context.Background(), frozenRequest(writtenAt().Add(ttl-time.Second)))
	if err != nil || first.Items[0].Outcome != execution.FrozenRenewalRenewed {
		t.Fatalf("first RenewFrozenRuntime() = (%+v, %v)", first, err)
	}
	if len(backend.renewals) != 1 {
		t.Fatalf("renewals after the first Slot = %d, want one", len(backend.renewals))
	}
	// The next Slot, with the blob unchanged -- a renewal does not rewrite it,
	// so the age it reports has grown, not reset.
	second, err := store.RenewFrozenRuntime(context.Background(), frozenRequest(writtenAt().Add(ttl)))
	if err != nil {
		t.Fatalf("second RenewFrozenRuntime() error = %v", err)
	}
	if len(backend.renewals) != 1 {
		t.Fatalf("renewals after the second Slot = %d, want still one. Age never resets, so without the "+
			"ask gate a frozen key is asked about every Slot forever", len(backend.renewals))
	}
	if second.Items[0].Outcome != execution.FrozenRenewalFresh {
		t.Fatalf("outcome = %q, want %q for a key the process already knows has life left",
			second.Items[0].Outcome, execution.FrozenRenewalFresh)
	}
}

// A renewal changes the key's life and nothing else.
//
// The stored bytes carry the blob revision and the mutation digest that every
// fenced write compares against. A renewal that touched them would turn a
// retry of the same Slot into a conflict, which is the failure this change
// exists to remove.
func TestARenewalLeavesTheStoredRecordByteIdentical(t *testing.T) {
	store, backend, key, ttl := frozenRenewalFixture(t)
	before := append([]byte(nil), backend.values[key]...)
	if len(before) == 0 {
		t.Fatal("the fixture stored nothing; there is no record to compare")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := store.RenewFrozenRuntime(context.Background(),
			frozenRequest(writtenAt().Add(ttl-time.Second))); err != nil {
			t.Fatalf("RenewFrozenRuntime() attempt %d error = %v", attempt, err)
		}
	}
	if string(backend.values[key]) != string(before) {
		t.Fatalf("the stored record changed from %q to %q; a renewal must not move the revision a "+
			"retry of the same Slot compares against", before, backend.values[key])
	}
}

// A store answers for every series it was asked about.
func TestAFrozenRenewalAnswersForEverySeries(t *testing.T) {
	store, _, _, ttl := frozenRenewalFixture(t)
	request := frozenRequest(writtenAt().Add(ttl - time.Second))
	second := stateIdentityV2()
	second.SeriesIdentityDigest = seriesDigest("second-series")
	request.Items = append(request.Items, execution.FrozenSeriesState{
		Identity: second, LastApplied: applyVersion().EvaluationTime,
		Representation: execution.StateRepresentationFramed,
	})
	result, err := store.RenewFrozenRuntime(context.Background(), request)
	if err != nil {
		t.Fatalf("RenewFrozenRuntime() error = %v", err)
	}
	if err := execution.ValidateFrozenStateRenewal(request, result); err != nil {
		t.Fatalf("result does not answer the request: %v", err)
	}
	// The second series was never written, so its key is not there. It reads
	// as a loss rather than as an absence of work.
	if result.Items[1].Outcome != execution.FrozenRenewalMissing {
		t.Fatalf("second outcome = %q, want %q", result.Items[1].Outcome, execution.FrozenRenewalMissing)
	}
}
