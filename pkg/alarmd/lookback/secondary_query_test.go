// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// secondary is a dependency query of qg's Slot, as the access layer marks
// it: another physical query, not the Slot's first read.
func secondary(slot int64) Query {
	q := query("qg", slot, minute, sourceLog)
	q.Spec.Digest = "qg-dependency"
	q.Spec.LogicalWindow = execution.QueryWindow{Start: slot - 86460, End: slot - 86400}
	q.Secondary = true
	return q
}

// A secondary query is counted among its source's first reads and takes no
// sample; the Slot's first read, after it, does (the h design, section 14).
func TestASecondaryQueryTakesNoSampleAndTheFirstReadDoes(t *testing.T) {
	f := newFixture(t)
	slot := f.clock.now().Unix()
	if read := f.engine.Begin(secondary(slot)); read == nil || read.query.Spec.Digest != "" {
		t.Fatalf("a secondary query took the sample: %+v", read)
	}
	stats := f.engine.Stats().Sources[sourceLog]
	if stats.FirstReads != 1 || stats.Samples[OutcomeCaptured] != 0 {
		t.Fatalf("first reads %d samples %v, want the secondary counted and nothing captured", stats.FirstReads, stats.Samples)
	}
	f.engine.mu.Lock()
	_, made := f.engine.groups["qg"]
	f.engine.mu.Unlock()
	if made {
		t.Fatal("a secondary query made its Query Group's state")
	}
	if read := f.engine.Begin(query("qg", slot, minute, sourceLog)); read == nil || read.query.Spec.Digest != "qg-query" {
		t.Fatalf("the Slot's first read did not take the sample after a secondary query: %+v", read)
	}
	// A secondary query of another step, after the first read, leaves the
	// group's step and source as the first read set them.
	other := secondary(slot)
	other.Spec.PlanFacts.StepMillis = (5 * time.Minute).Milliseconds()
	other.Spec.PlanFacts.SourceSemantics = []string{"bk_monitor/time_series"}
	f.engine.Begin(other)
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	if state := f.engine.groups["qg"]; state.step != minute || state.source != sourceLog {
		t.Fatalf("group step %s source %s after a secondary query, want the first read's %s %s", state.step, state.source, minute, sourceLog)
	}
}

// The trial read that lowers h is reserved for the Slot's first read alone.
// A secondary query neither takes the reservation nor, coming second, gives
// it up; giving it up was why a group with a dependency never lowered its
// hold.
func TestASecondaryQueryReservesNoTrialRead(t *testing.T) {
	for _, order := range []string{"secondary first", "first read first"} {
		t.Run(order, func(t *testing.T) {
			f := newFixture(t)
			f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
			slot := f.clock.now().Unix()
			held := func(q Query) Query {
				q.Contract.ReadHoldMillis = (300 * time.Second).Milliseconds()
				q.ReadyAt = time.Unix(slot, 0).Add(10*time.Second + 300*time.Second)
				return q
			}
			first, dependency := held(query("qg", slot, minute, sourceLog)), held(secondary(slot))
			if order == "secondary first" {
				f.engine.Prepare(dependency)
				f.engine.Prepare(first)
			} else {
				f.engine.Prepare(first)
				f.engine.Prepare(dependency)
			}
			f.engine.mu.Lock()
			defer f.engine.mu.Unlock()
			state := f.engine.groups["qg"]
			if state == nil || state.prepared == nil {
				t.Fatal("no trial read reserved for the Slot's first read")
			}
			if state.prepared.query.Spec.Digest != "qg-query" || state.prepared.done {
				t.Fatalf("trial reserved for %s, done %v (%s), want the first read's, still pending",
					state.prepared.query.Spec.Digest, state.prepared.done, state.prepared.outcome)
			}
		})
	}
}

// A directed Slot still records a secondary query and is set aside as
// multi_query: its supplement replays one kept read per physical query, and
// a directed read of the first read alone keeps nothing of the dependency
// to replay. The first-read rule moves the sample and the trial read, not
// this.
func TestADirectedSlotWithASecondaryQueryIsStillSetAside(t *testing.T) {
	f, recorder := directedFixture(t, SupplementOutcome{Ran: true})
	slot := f.clock.now().Unix()
	read := f.engine.Begin(secondary(slot))
	if read == nil || read.directed == nil {
		t.Fatalf("a directed Slot did not record its secondary query: %+v", read)
	}
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	f.firstRead(slot, execution.CompletenessFull)
	f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
	f.engine.Step(context.Background())
	stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedUnobserved] == 1 })
	if got := stats.Sources[sourceLog].SupplementUnobserved; got[UnobservedMultiQuery] != 1 || len(recorder.taken()) != 0 {
		t.Fatalf("unobserved %v jobs %d, want the Slot set aside as multi_query and no supplement", got, len(recorder.taken()))
	}
}

// Only a normal operation's first attempt is a first read. A retry of the
// Slot, a replay, a probe and a supplement (which replays a kept read)
// take no sample and reserve no trial read, whatever the access layer
// marked, so a Slot is sampled and trial-read once.
func TestOnlyANormalFirstAttemptIsAFirstRead(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation execution.Operation
		attempt   uint32
	}{
		{"second attempt", execution.OperationNormal, 2},
		{"retry", execution.OperationRetry, 1},
		{"replay", execution.OperationReplay, 1},
		{"probe", execution.OperationProbe, 1},
		{"supplement", execution.OperationSupplement, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
			slot := f.clock.now().Unix()
			q := query("qg", slot, minute, sourceLog)
			q.Operation, q.AttemptNo = tc.operation, tc.attempt
			q.Contract.ReadHoldMillis = (300 * time.Second).Milliseconds()
			q.ReadyAt = time.Unix(slot, 0).Add(310 * time.Second)
			f.engine.Prepare(q)
			if read := f.engine.Begin(q); read != nil {
				t.Fatalf("Begin = %+v, want no read kept", read)
			}
			f.engine.mu.Lock()
			defer f.engine.mu.Unlock()
			if state := f.engine.groups["qg"]; state != nil && (state.prepared != nil || state.capturing || state.sample != nil) {
				t.Fatalf("prepared %v capturing %v sample %v, want nothing taken", state.prepared != nil, state.capturing, state.sample != nil)
			}
		})
	}
}
