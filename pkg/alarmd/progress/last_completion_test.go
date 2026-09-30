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

// The committed round is kept on the record, and the next one replaces it.
//
// LastCompletionKind was one word of this and was all there was: a reader with
// a Query Group that stopped could see that the last round ended in a gap and
// not which Slot, when, why, or under which frozen contract -- and the round
// that would have said so had scrolled out of the log by the time anybody
// looked. Every field here was in hand at the commit, so none of it costs a
// read.
func TestTheCommittedRoundIsKeptAndTheNextOneReplacesIt(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	gapped := execution.SlotCompletion{
		Contract: progressContract(), Kind: execution.CompletionPartialGap,
		Primary:    &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
		Result:     observability.ResultDegraded,
		ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
	}
	if result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 60, Completion: gapped,
	}); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v)", result, err)
	}

	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	summary := loaded.Progress.LastCompletion
	if summary == nil {
		t.Fatal("no last completion on a record this process just committed")
	}
	if summary.Slot != 60 {
		t.Fatalf("Slot = %d, want the Slot that completed and not the one the cursor moved to", summary.Slot)
	}
	if summary.Kind != execution.CompletionPartialGap ||
		summary.ReasonCode != execution.ReasonCode(contract.ReasonHistoryWarming) {
		t.Fatalf("summary = %+v, want the kind and reason the round reported", summary)
	}
	if summary.Contract != progressContract() {
		t.Fatalf("Contract = %+v, want the frozen contract the round ran under: without it the summary "+
			"cannot be told from one written by another generation of the same Query Group", summary.Contract)
	}
	// The commit's clock, not the Slot's: the question is how long ago
	// anything happened here, and a Slot time answers that only for a
	// deployment that is keeping up -- which is not the one being looked at.
	if summary.CompletedAt != "1970-01-01T00:00:01Z" {
		t.Fatalf("CompletedAt = %q, want the instant the commit was made", summary.CompletedAt)
	}

	// The next round replaces it whole. Two summaries kept side by side would
	// be two answers to a question that has one.
	full := execution.SlotCompletion{
		Contract: progressContractAt(120), Kind: execution.CompletionFull,
		Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
		Result:  observability.ResultSuccess,
	}
	if result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120, Completion: full,
	}); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("second CommitProgress() = (%+v, %v)", result, err)
	}
	loaded, err = store.LoadProgress(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Progress.LastCompletion.Slot != 120 ||
		loaded.Progress.LastCompletion.Kind != execution.CompletionFull ||
		loaded.Progress.LastCompletion.ReasonCode != "" {
		t.Fatalf("summary = %+v, want the round that just committed and nothing of the one before it",
			loaded.Progress.LastCompletion)
	}
}

// A record written before this field existed reads as no summary, not as a
// completion at Slot zero.
//
// Every deployment has these on the round it upgrades, and a zero-valued
// summary would name Slot 0 with an empty contract -- a completion that never
// happened, indistinguishable on the page from one that did.
func TestARecordWrittenBeforeTheSummaryReadsAsNone(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	identity := execution.ProgressIdentity{QueryGroup: "q"}

	// The bytes an older build wrote: the same schema, without the field.
	older, err := encode(execution.ScheduleProgress{
		Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
	})
	if err != nil {
		t.Fatal(err)
	}
	var withoutField map[string]any
	if err := json.Unmarshal(older, &withoutField); err != nil {
		t.Fatal(err)
	}
	progress := withoutField["progress"].(map[string]any)
	if _, present := progress["last_completion"]; present {
		t.Fatal("fixture: the record carries the field, so this reads nothing about a record without it")
	}
	fake.missing, fake.value = false, older

	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil {
		t.Fatalf("LoadProgress() = %v; a record written before the field must still load", err)
	}
	if loaded.Progress.LastCompletion != nil {
		t.Fatalf("LastCompletion = %+v, want none: this process has not committed a round for this "+
			"Query Group since the upgrade, and that is the answer", loaded.Progress.LastCompletion)
	}
	if loaded.Progress.NextSlot != 120 || loaded.Progress.LastCompletionKind != execution.CompletionFull {
		t.Fatalf("the rest of the record did not survive: %+v", loaded.Progress)
	}
}

// The names on the wire are the contract the page reads by.
//
// They are asserted against the encoded bytes rather than against the struct,
// because a tag is what decides them and a tag is what a refactor silently
// changes: renaming the Go field would keep every other test in this package
// green while the page stopped finding the object.
func TestTheSummaryKeepsTheNamesThePageReadsBy(t *testing.T) {
	encoded, err := encode(execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: "q"}, NextSlot: 120, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
		LastCompletion: &execution.LastCompletionSummary{
			Slot: 60, CompletedAt: "1970-01-01T00:00:01Z", Kind: execution.CompletionFull,
			Contract: progressContract(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Progress struct {
			LastCompletion map[string]json.RawMessage `json:"last_completion"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(envelope.Progress.LastCompletion))
	for name := range envelope.Progress.LastCompletion {
		names = append(names, name)
	}
	want := map[string]struct{}{"slot": {}, "completed_at": {}, "kind": {}, "contract": {}}
	for _, name := range names {
		if _, expected := want[name]; !expected {
			t.Fatalf("the summary wrote %q, which the page does not read", name)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("the summary did not write %v; the page finds nothing under a name that moved", want)
	}
	// reason_code is deliberately not in that set: it is omitted when the
	// completion had none, so requiring it would fail on every clean round.
}
