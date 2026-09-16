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
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// What the gate saves is the round trip, which is a different saving from what
// the script saves.
//
// The script's threshold means a load usually writes no new expiry; the command
// goes out either way and the process waits for the answer. Measured, that ask
// is the whole cost: 38.6 EVAL/s at 7.1ms each on around 2,100 Plans. So this
// asserts the backend was not called -- a test that read the returned value, or
// that the key was not renewed, would pass just as well against the version
// that sends the command and is told no.
func TestASecondLoadInsideTheIntervalDoesNotReachTheBackend(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte), remaining: make(map[string]time.Duration)}
	store := generationStore(t, backend)
	item := gapLoadItem("generation", nil)
	key, err := PlanGapKeyV2("alarmd", item.Identity)
	if err != nil {
		t.Fatal(err)
	}
	backend.values[key] = []byte("{}")
	backend.remaining[key] = GenerationScopedFloor

	load := func() {
		t.Helper()
		if _, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{
			Contract: frozenRef(), Items: []execution.PlanGapLoadItem{item},
		}); err != nil {
			t.Fatal(err)
		}
	}

	load()
	if len(backend.renewals) != 1 {
		t.Fatalf("first load made %d renewal calls, want the one that establishes the key's life",
			len(backend.renewals))
	}
	// Every Slot for the next six hours.
	for round := 0; round < 200; round++ {
		load()
	}
	if len(backend.renewals) != 1 {
		t.Fatalf("renewal calls after 201 loads = %d, want 1: the script decides inside Redis, so every "+
			"call that reaches it has already spent the round trip this gate exists to save",
			len(backend.renewals))
	}
	// The load itself still happens: the gate is about the renewal, not about
	// reading the record, and a gate that skipped the read would stop the Plan
	// from being evaluated at all.
	if backend.reads < 201 {
		t.Fatalf("reads = %d, want one per load: the record is still read every Slot", backend.reads)
	}
}

// Once the interval is up, the ask goes out again.
//
// A gate that never reopened would be the leak this whole mechanism exists to
// close, arriving by a different route and with the same symptom: the keys work
// until they do not.
func TestTheAskGoesOutAgainOnceTheIntervalHasPassed(t *testing.T) {
	gate := newRenewalGate()
	clock := time.Unix(1_700_000_000, 0).UTC()
	gate.now = func() time.Time { return clock }
	interval := RenewalAskInterval(GenerationScopedFloor)

	if !gate.Ask("k") {
		t.Fatal("a key the gate has never seen was not asked about")
	}
	gate.Answered("k", interval)
	if gate.Ask("k") {
		t.Fatal("the key was asked about again immediately")
	}
	clock = clock.Add(interval - time.Second)
	if gate.Ask("k") {
		t.Fatal("the key was asked about a second before the interval was up")
	}
	clock = clock.Add(time.Second)
	if !gate.Ask("k") {
		t.Fatalf("the key was not asked about after %s; nothing else renews it", interval)
	}
}

// The interval is half of what an answered ask guarantees.
//
// After an ask that returned, the key has at least the threshold left: either
// the script renewed it to a full life, or it declined because at least the
// threshold remained. Spending all of that on skipping would arrive at the next
// ask with nothing left. This is the arithmetic, stated where it can fail.
func TestTheAskIntervalSpendsHalfOfWhatAnAnswerGuarantees(t *testing.T) {
	for _, ttl := range []time.Duration{GenerationScopedFloor, 30 * 24 * time.Hour, 2 * time.Hour} {
		guaranteed := GenerationScopedRenewalThreshold(ttl)
		interval := RenewalAskInterval(ttl)
		if interval <= 0 {
			t.Fatalf("interval for a %s life is %s; a non-positive interval asks every time", ttl, interval)
		}
		if interval*2 > guaranteed {
			t.Fatalf("at a %s life the gate skips %s but an answered ask only guarantees %s",
				ttl, interval, guaranteed)
		}
		// And it does not put the script's own branches out of reach: at the
		// interval the key still has more than the threshold, so the decline
		// branch is reached, and by the third ask it is below and the renew
		// branch is reached. A gate wide enough to leave only one of them alive
		// would have deleted the thing it was tuned against.
		if ttl-interval < guaranteed {
			t.Fatalf("at a %s life the first ask after the interval already has less than the threshold "+
				"left, so the script never declines and the gate is doing the script's job", ttl)
		}
		if ttl-3*interval >= guaranteed {
			t.Fatalf("at a %s life three intervals still leave more than the threshold, so the renew "+
				"branch is not reached in the rounds this bound covers", ttl)
		}
	}
}

// A failed ask is not an answer.
//
// The gate spends a guarantee that only a returned ask provides. Recording a
// call that errored would skip on the strength of an answer nobody got, and the
// keys it skipped are the ones whose store was in trouble.
func TestAFailedAskIsNotRecorded(t *testing.T) {
	backend := &failingRenewalBackend{casMemoryBackend: casMemoryBackend{
		values: make(map[string][]byte), remaining: make(map[string]time.Duration),
	}}
	store := generationStore(t, &backend.casMemoryBackend)
	store.renewals = newRenewalGate()
	key, err := PlanGapKeyV2("alarmd", gapLoadItem("generation", nil).Identity)
	if err != nil {
		t.Fatal(err)
	}
	backend.values[key] = []byte("{}")

	for round := 0; round < 3; round++ {
		if err := RenewGenerationKey(context.Background(), StorageTarget{Backend: backend}, key, nil,
			time.Minute, time.Minute, 30*24*time.Hour, store.renewals); err == nil {
			t.Fatal("a failing backend renewed without error")
		}
	}
	if backend.attempts != 3 {
		t.Fatalf("attempts = %d, want one per load: a call that failed leaves nothing to skip on",
			backend.attempts)
	}
}

// A backend that cannot renew says so on every load, not once per interval.
//
// That error is what stops a Slot running against a store where generation keys
// leak. Behind the gate it would be reported on the first load and then hidden
// for six hours at a time, which restores the leak and the silence together.
func TestACapabilityMissIsReportedOnEveryLoad(t *testing.T) {
	gate := newRenewalGate()
	for round := 0; round < 3; round++ {
		err := RenewGenerationKey(context.Background(), StorageTarget{Backend: lifetimelessBackend{}},
			"key", nil, time.Minute, time.Minute, 30*24*time.Hour, gate)
		if !errors.Is(err, ErrLifetimeUnsupported) {
			t.Fatalf("round %d returned %v, want the capability miss on every load", round, err)
		}
	}
}

// A table under pressure drops what has come due and keeps what has not.
//
// This is the difference between a gate that works on a large deployment and
// one that only works on a small one. Dropping a due entry changes nothing --
// the next load was going to ask about it anyway -- so it is always the right
// thing to drop first, and on a real worker it is most of the table: the keys
// of every generation a rollout replaced leave here.
func TestAFullTableDropsWhatIsDueAndKeepsWhatIsNot(t *testing.T) {
	gate := newRenewalGate()
	clock := time.Unix(1_700_000_000, 0).UTC()
	gate.now = func() time.Time { return clock }
	interval := RenewalAskInterval(GenerationScopedFloor)

	// Half the table was answered an interval ago and has come due; half was
	// answered just now and has not.
	due := make([]string, 0, renewalGateSweepFloor/2)
	for index := 0; index < renewalGateSweepFloor/2; index++ {
		key := fmt.Sprintf("due-%d", index)
		gate.Answered(key, interval)
		due = append(due, key)
	}
	clock = clock.Add(interval)
	live := make([]string, 0, renewalGateSweepFloor/2)
	for index := 0; index < renewalGateSweepFloor/2; index++ {
		key := fmt.Sprintf("live-%d", index)
		gate.Answered(key, interval)
		live = append(live, key)
	}
	if len(gate.expiry) != renewalGateSweepFloor {
		t.Fatalf("table = %d entries, want the sweep trigger %d; the next insert is the one that sweeps",
			len(gate.expiry), renewalGateSweepFloor)
	}

	gate.Answered("one-more", interval)

	if gate.resets != 0 {
		t.Fatalf("resets = %d, want none: half the table had come due and dropping it made room", gate.resets)
	}
	for _, key := range live {
		if gate.Ask(key) {
			t.Fatalf("key %s was answered this instant and the table forgot it; only entries that have "+
				"come due may be dropped, because dropping those changes no behaviour", key)
		}
	}
	for _, key := range due {
		if !gate.Ask(key) {
			t.Fatalf("key %s had come due and is still being skipped", key)
		}
	}
	if _, present := gate.expiry[due[0]]; present {
		t.Fatalf("a due entry was kept; the sweep made no room and the table will clear next time")
	}
}

// Sweeping does not fall on every insert once the table is large.
//
// A gate that swept on every Answered past its trigger would spend a full pass
// over the table per load. The trigger tracks what survived, so the cost is
// spread over as many inserts as the table holds.
func TestTheSweepTriggerFollowsTheLiveWorkingSet(t *testing.T) {
	gate := newRenewalGate()
	clock := time.Unix(1_700_000_000, 0).UTC()
	gate.now = func() time.Time { return clock }
	interval := RenewalAskInterval(GenerationScopedFloor)

	for index := 0; index <= renewalGateSweepFloor; index++ {
		gate.Answered(fmt.Sprintf("live-%d", index), interval)
	}
	// Nothing had come due, so the sweep freed nothing and the trigger moved
	// out to twice what is live rather than staying where every further insert
	// would sweep again.
	if gate.sweepAt <= renewalGateSweepFloor {
		t.Fatalf("sweepAt = %d after a sweep that freed nothing, want it past the floor %d: otherwise "+
			"every later insert sweeps the whole table", gate.sweepAt, renewalGateSweepFloor)
	}
	if gate.resets != 0 {
		t.Fatalf("resets = %d, want none well below the ceiling", gate.resets)
	}
}

// Only the ceiling clears the table, and clearing it is counted.
//
// At the ceiling everything in the table is live, so there is nothing to drop
// that would not change behaviour. Forgetting all of it costs one round of
// asking and no wrong answers -- but it is the whole cost the gate exists to
// avoid, paid at once, and nothing else in the process says it happened.
func TestOnlyTheCeilingClearsTheTableAndSaysSo(t *testing.T) {
	gate := newRenewalGate()
	clock := time.Unix(1_700_000_000, 0).UTC()
	gate.now = func() time.Time { return clock }
	interval := RenewalAskInterval(GenerationScopedFloor)
	// Fill past the ceiling with entries that are all live, which is the only
	// state a sweep cannot help with.
	for index := 0; index <= renewalGateCeiling; index++ {
		gate.Answered(fmt.Sprintf("live-%d", index), interval)
	}

	if gate.Resets() != 1 {
		t.Fatalf("resets = %d, want exactly one: the table passed the ceiling once", gate.Resets())
	}
	if len(gate.expiry) > renewalGateCeiling {
		t.Fatalf("table = %d entries, want it cleared at the ceiling", len(gate.expiry))
	}
	// And it is asking again, which is what a reset means.
	if !gate.Ask("live-0") {
		t.Fatal("a key from before the reset is still being skipped")
	}
}

// The ceiling covers every Plan of the largest deployment on one worker.
//
// This is the arithmetic the number was chosen from, stated where it fails if
// the shape changes: two generation-scoped keys per Plan, and a deployment
// around forty-eight times the one this was measured on.
func TestTheCeilingCoversTheLargestDeploymentOnOneWorker(t *testing.T) {
	const largestDeploymentPlans = 100000
	const generationScopedKeysPerPlan = 2
	if want := largestDeploymentPlans * generationScopedKeysPerPlan; renewalGateCeiling < want {
		t.Fatalf("ceiling %d is below the %d keys a single worker would hold if it owned every Plan of "+
			"the largest deployment; past it the gate clears on every answer and saves nothing",
			renewalGateCeiling, want)
	}
	// And the sweep floor is far below it, so an ordinary deployment never
	// reaches the ceiling path at all.
	if renewalGateSweepFloor >= renewalGateCeiling {
		t.Fatalf("sweep floor %d is not below the ceiling %d", renewalGateSweepFloor, renewalGateCeiling)
	}
}

// failingRenewalBackend renews nothing and counts the attempts.
type failingRenewalBackend struct {
	casMemoryBackend
	attempts int
}

func (backend *failingRenewalBackend) RenewIfBelow(
	_ context.Context, _ string, _, _ time.Duration,
) (bool, error) {
	backend.attempts++
	return false, errors.New("state: renewal failed")
}

// lifetimelessBackend is a routed backend with no lifetime support at all.
type lifetimelessBackend struct{}

func (lifetimelessBackend) MGet(_ context.Context, keys []string) ([][]byte, error) {
	return make([][]byte, len(keys)), nil
}

func (lifetimelessBackend) SetMany(_ context.Context, _ []BackendWrite) error {
	return errors.New("state: not supported")
}
