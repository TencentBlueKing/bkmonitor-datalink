// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package progress

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// emptyRunHarness commits rounds of every kind for one object and reads the
// two facts back after each.
type emptyRunHarness struct {
	t        *testing.T
	store    *Store
	identity execution.ProgressIdentity
	fence    execution.OwnerFence
}

func newEmptyRunHarness(t *testing.T, fake *controlFake) *emptyRunHarness {
	t.Helper()
	return &emptyRunHarness{t: t, store: mustStore(t, fake),
		identity: execution.ProgressIdentity{QueryGroup: "q"},
		fence:    execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}}
}

func (h *emptyRunHarness) commit(slot execution.EvaluationTime, kind execution.CompletionKind) {
	h.t.Helper()
	completion := execution.SlotCompletion{Contract: progressContractAt(slot), Kind: kind}
	switch kind {
	case execution.CompletionFull:
		completion.Primary = &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}
		completion.Result = observability.ResultSuccess
	case execution.CompletionFullEmpty:
		completion.Primary = &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty}
		completion.Result = observability.ResultSuccess
	case execution.CompletionPartialGap:
		completion.Primary = &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}
		completion.Result, completion.ReasonCode = observability.ResultDegraded, execution.ReasonCode(contract.ReasonHistoryWarming)
	case execution.CompletionGapSkipped:
		completion.Result, completion.ReasonCode = observability.ResultDegraded, execution.ReasonCode(contract.ReasonGapSkipped)
	default:
		h.t.Fatalf("harness: no fixture for %s", kind)
	}
	result, err := h.store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: h.identity, OwnerFence: h.fence, ExpectedNextSlot: slot, Completion: completion})
	if err != nil || result.Status != execution.ProgressCommitted {
		h.t.Fatalf("CommitProgress(%s at %d) = (%+v, %v)", kind, slot, result, err)
	}
}

func (h *emptyRunHarness) facts(step string, wantData, wantSince execution.EvaluationTime) {
	h.t.Helper()
	loaded, err := h.store.LoadProgress(context.Background(), h.identity)
	if err != nil || loaded.Progress == nil {
		h.t.Fatalf("LoadProgress(%s) = (%+v, %v)", step, loaded, err)
	}
	if loaded.Progress.LastDataSlot != wantData || loaded.Progress.EmptyRunSinceSlot != wantSince {
		h.t.Errorf("%s: LastDataSlot = %d, EmptyRunSinceSlot = %d; want %d and %d",
			step, loaded.Progress.LastDataSlot, loaded.Progress.EmptyRunSinceSlot, wantData, wantSince)
	}
}

// The record keeps the last Slot that completed with records and the first
// Slot of the run of empty rounds it is in, so a replica that restores the
// object after a release knows both without watching a round.
//
// Only records end the run: a gap or a warming round between two empty ones
// is not evidence of records, and the run's start stands through it. Both
// values are Slots, so a retry of the same Slot writes the same value -- the
// commit's clock is deliberately not used. LastFullSlot cannot serve for
// either: the empty round below advances it too.
func TestTheRecordKeepsWhenRecordsWereLastSeenAndWhenTheEmptyRunBegan(t *testing.T) {
	h := newEmptyRunHarness(t, &controlFake{missing: true})
	h.commit(60, execution.CompletionFullEmpty)
	h.facts("first empty round on a fresh record", 0, 60)
	h.commit(120, execution.CompletionFullEmpty)
	h.facts("second empty round keeps the run's start", 0, 60)
	h.commit(180, execution.CompletionGapSkipped)
	h.facts("a skip is not records: the run stands", 0, 60)
	h.commit(240, execution.CompletionPartialGap)
	h.facts("a warming round is not records either", 0, 60)
	h.commit(300, execution.CompletionFullEmpty)
	h.facts("empty again after the interruptions, same run", 0, 60)
	h.commit(360, execution.CompletionFull)
	h.facts("records end the run and are remembered", 360, 0)
	h.commit(420, execution.CompletionFullEmpty)
	h.facts("a new run after records, records still remembered", 360, 420)
	h.commit(480, execution.CompletionGapSkipped)
	h.facts("skipped after the new run, both kept", 360, 420)

	loaded, err := h.store.LoadProgress(context.Background(), h.identity)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Progress.LastFullSlot != 420 {
		t.Fatalf("LastFullSlot = %d: the fixture was to show it advancing on the empty round at 420, which is why it cannot stand in for either fact",
			loaded.Progress.LastFullSlot)
	}
}

// A build without the fields reads the record and writes it back without
// them -- on Begin, Commit and Skip alike -- so during a mixed-version roll
// the replica that owns an object last may drop both. The next build then
// reads zero. Zero LastDataSlot is "no Slot known to have had records", and
// stays so until records arrive; a zero run start under an empty last round
// cannot be this build's own writing, so the run is dated from the earliest
// empty Slot the record can still prove -- the summary's -- rather than from
// the round being committed. A lower bound either way: the hour fires late,
// never early.
func TestARecordWrittenBackByABuildWithoutTheFieldsRestartsFromWhatItCanProve(t *testing.T) {
	fake := &controlFake{missing: true}
	h := newEmptyRunHarness(t, fake)
	h.commit(60, execution.CompletionFull)
	h.commit(120, execution.CompletionFullEmpty)
	h.commit(180, execution.CompletionFullEmpty)
	h.facts("this build's own record", 60, 120)

	// The bytes the build without the fields writes back: the same record
	// with the two keys gone, as if it had committed the round at 180 itself.
	var envelope map[string]any
	if err := json.Unmarshal(fake.value, &envelope); err != nil {
		t.Fatal(err)
	}
	record := envelope["progress"].(map[string]any)
	for _, key := range []string{"LastDataSlot", "EmptyRunSinceSlot"} {
		if _, present := record[key]; !present {
			t.Fatalf("fixture: %q is not on the record this build wrote, so deleting it proves nothing", key)
		}
		delete(record, key)
	}
	rewritten, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	fake.value = rewritten
	h.facts("after the build without the fields wrote it back", 0, 0)

	// This build commits the next empty round: the run is dated from the
	// summary's Slot, 180, not from 240 -- and the records at 60 stay lost.
	h.commit(240, execution.CompletionFullEmpty)
	h.facts("the run restarts from the earliest empty Slot the record proves", 0, 180)

	// A summary whose last round was not empty proves no earlier empty Slot:
	// the run starts at the round being committed.
	fake.missing, fake.value = true, nil
	h = newEmptyRunHarness(t, fake)
	h.commit(60, execution.CompletionFull)
	if err := json.Unmarshal(fake.value, &envelope); err != nil {
		t.Fatal(err)
	}
	delete(envelope["progress"].(map[string]any), "LastDataSlot")
	if fake.value, err = json.Marshal(envelope); err != nil {
		t.Fatal(err)
	}
	h.commit(120, execution.CompletionFullEmpty)
	h.facts("a lost record of records under a round with records: the run starts here", 0, 120)
}

// Both fields are absent from the bytes at zero. That is what keeps every
// record this build has not touched byte-identical to what the build before
// wrote, and a record written by that build loads with both at zero rather
// than failing to load.
func TestTheTwoFactsAreAbsentFromTheBytesAtZero(t *testing.T) {
	encoded, err := encode(execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: "q"}, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull})
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Progress map[string]json.RawMessage `json:"progress"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"LastDataSlot", "EmptyRunSinceSlot"} {
		if _, present := envelope.Progress[key]; present {
			t.Errorf("%q is written at zero; a record this build never touched no longer matches the bytes the build before wrote", key)
		}
	}
	encoded, err = encode(execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: "q"}, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFullEmpty,
		LastDataSlot: 30, EmptyRunSinceSlot: 60})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"LastDataSlot", "EmptyRunSinceSlot"} {
		if _, present := envelope.Progress[key]; !present {
			t.Errorf("%q is not written when set; the fleet page restores nothing from it", key)
		}
	}
}

// Begin re-encodes the record it decoded, and the pruned skip rebuilds the
// record from scratch: the first keeps both facts because it keeps
// everything, the second has to be told to. It drops the continuity anchors
// on purpose, and neither fact is one.
func TestBeginAndThePrunedSkipKeepBothFacts(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	current := execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFullEmpty, LastDataSlot: 30, EmptyRunSinceSlot: 60}
	fake := &controlFake{value: mustEncode(t, current)}
	store := mustStore(t, fake)
	ctx := context.Background()

	if result, err := store.BeginSlot(ctx, execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence,
		Projection: progressProjectionAt(120)}); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(120) = (%+v, %v)", result, err)
	}
	loaded, err := store.LoadProgress(ctx, identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.UnfinishedSlot == nil {
		t.Fatalf("LoadProgress(after Begin) = (%+v, %v)", loaded, err)
	}
	if loaded.Progress.LastDataSlot != 30 || loaded.Progress.EmptyRunSinceSlot != 60 {
		t.Errorf("after Begin: LastDataSlot = %d, EmptyRunSinceSlot = %d; want 30 and 60 kept", loaded.Progress.LastDataSlot, loaded.Progress.EmptyRunSinceSlot)
	}

	fake.value = mustEncode(t, current)
	if result, err := store.SkipPrunedRange(ctx, execution.ProgressSkipPrunedRequest{Identity: identity, OwnerFence: fence,
		ExpectedNextSlot: 120, ResumeAt: 600}); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("SkipPrunedRange() = (%+v, %v)", result, err)
	}
	loaded, err = store.LoadProgress(ctx, identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress(after skip) = (%+v, %v)", loaded, err)
	}
	if loaded.Progress.LastFullSlot != 0 {
		t.Fatalf("after the pruned skip LastFullSlot = %d: the fixture is that the anchors go, so keeping the facts is a decision and not a default", loaded.Progress.LastFullSlot)
	}
	if loaded.Progress.LastDataSlot != 30 || loaded.Progress.EmptyRunSinceSlot != 60 {
		t.Errorf("after the pruned skip: LastDataSlot = %d, EmptyRunSinceSlot = %d; want 30 and 60 kept", loaded.Progress.LastDataSlot, loaded.Progress.EmptyRunSinceSlot)
	}
}
