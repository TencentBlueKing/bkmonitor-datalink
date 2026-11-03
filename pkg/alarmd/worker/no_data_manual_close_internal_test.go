// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// closingNoDataStore answers like horizonNoDataStore and also carries the
// strategy's manual closes on the snapshot, as the Redis load does.
type closingNoDataStore struct {
	horizonNoDataStore
	closes map[string]int64
	// unread answers as a load whose closes could not be read.
	unread bool
}

func (store *closingNoDataStore) LoadNoData(
	ctx context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	result, err := store.horizonNoDataStore.LoadNoData(ctx, request)
	for index := range result.Items {
		result.Items[index].ManualCloses = store.closes
		if store.unread && result.Items[index].Status == execution.NoDataMemoryFound {
			result.Items[index].ManualClosesUnread = true
		}
	}
	return result, err
}

// manualCloseDue is the wired no-data Plan with what a production Plan has
// and the shared fixture leaves out: a frozen snapshot revision and an
// output identity, without which nothing is fingerprinted.
func manualCloseDue(t *testing.T) execution.DuePlan {
	t.Helper()
	due := noDataWiredPlan(t)
	due.CompiledPlan = noDataPreflightPlanWithIdentity(t,
		contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "strategy-v1", SnapshotRevision: 1},
		&contract.NoDataConfigV1{Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"}},
		&contract.MonitorOutputIdentity{DimensionFields: []string{"host"}})
	return due
}

const closedGroupKey = "bk_target_cloud_id=0,bk_target_ip=192.0.2.10," + contract.NoDataDimensionTag + "=true"

func closedGroupFingerprint(t *testing.T, due execution.DuePlan) string {
	t.Helper()
	group, ok := nodata.ParseGroupKey(closedGroupKey)
	if !ok {
		t.Fatal("fixture group key does not parse")
	}
	identity := noDataOutputIdentity(due)
	if identity == nil {
		t.Fatal("the no-data Plan has no output identity")
	}
	fingerprint, err := nodata.GroupFingerprint(due.Identity.StrategyID, due.Identity.BusinessID, *identity, group)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

// decision-024 section 5.1: a close stops the absence it was recorded for at
// the Plan's next round, and only that one.
func TestAManualCloseStopsTheAbsenceItWasRecordedFor(t *testing.T) {
	due := manualCloseDue(t)
	evaluation := int64(noDataPreflightContract(t, []execution.DuePlan{due}).Slot.EvaluationTime)
	firstAbsent := evaluation - 1800
	group := execution.NoDataGroupMemory{GroupKey: closedGroupKey, LastSeen: firstAbsent - 60, FirstAbsent: firstAbsent}
	fingerprint := closedGroupFingerprint(t, due)
	for _, testCase := range []struct {
		name     string
		closedAt int64
		stopped  bool
	}{
		{name: "closed after the absence began", closedAt: firstAbsent + 60, stopped: true},
		// A close recorded before this absence began was about an earlier
		// one; the data came back since, and this absence is tracked.
		{name: "closed before this absence began", closedAt: firstAbsent - 600, stopped: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &closingNoDataStore{
				horizonNoDataStore: horizonNoDataStore{groups: []execution.NoDataGroupMemory{group}, present: group.LastSeen},
				closes:             map[string]int64{fingerprint: testCase.closedAt},
			}
			stream := noDataWiredStream(t, due, store)
			if err := stream.loadNoDataMemory(context.Background()); err != nil {
				t.Fatal(err)
			}
			round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
			if err != nil {
				t.Fatal(err)
			}
			absentSent := len(round.absenceStarts) > 0
			// Nothing absent is sent, the whole-item group included: closing a
			// history roster's last group must not open the item's own absence.
			if absentSent == testCase.stopped || (round.facts.ManualClosed == 1) != testCase.stopped {
				t.Fatalf("absent series sent = %t, manual_closed = %d; want stopped = %t",
					absentSent, round.facts.ManualClosed, testCase.stopped)
			}
			if !testCase.stopped {
				return
			}
			recorded := false
			if round.mutation != nil {
				for _, written := range round.mutation.Set {
					if written.GroupKey == closedGroupKey && written.Absent != nil && written.Absent.SuppressedAt == evaluation {
						recorded = true
					}
				}
			}
			if !recorded {
				t.Fatalf("the stop was not written to the memory: %+v", round.mutation)
			}
		})
	}
}

// decision-024 section 2: the fingerprint a close is matched by is the one the
// trigger computes for the round's own record, through the view the trigger
// is handed.
func TestTheRecomputedFingerprintIsTheOneTheTriggerComputes(t *testing.T) {
	due := manualCloseDue(t)
	evaluation := int64(noDataPreflightContract(t, []execution.DuePlan{due}).Slot.EvaluationTime)
	group := execution.NoDataGroupMemory{GroupKey: closedGroupKey, LastSeen: evaluation - 1860, FirstAbsent: evaluation - 1800}
	store := &horizonNoDataStore{groups: []execution.NoDataGroupMemory{group}, present: group.LastSeen}
	stream := noDataWiredStream(t, due, store)
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
	if err != nil || len(round.series) == 0 {
		t.Fatalf("the round produced no series: %v", err)
	}
	view, err := execution.PlanViewFor(due, execution.SeriesKindNoData)
	if err != nil {
		t.Fatal(err)
	}
	record := round.series[0].inputs[0].Inputs[0].Dataset.Records()[0]
	trigger, err := contract.MonitorDedupeMD5(due.Identity.StrategyID, record.BusinessID, record.Dimensions,
		*view.CompiledPlan.OutputIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if recomputed := closedGroupFingerprint(t, due); recomputed != trigger {
		t.Fatalf("recomputed fingerprint %s, the trigger's %s", recomputed, trigger)
	}
}

// A memory read whose closes could not be read says so on the read's line, which
// is what read_failures{site="memory_load"} counts: in an outage the counter
// must move, not read zero.
func TestAMemoryLoadWithoutItsClosesSaysSo(t *testing.T) {
	due := manualCloseDue(t)
	evaluation := int64(noDataPreflightContract(t, []execution.DuePlan{due}).Slot.EvaluationTime)
	group := execution.NoDataGroupMemory{GroupKey: closedGroupKey, LastSeen: evaluation - 1860, FirstAbsent: evaluation - 1800}
	store := &closingNoDataStore{horizonNoDataStore: horizonNoDataStore{groups: []execution.NoDataGroupMemory{group}, present: group.LastSeen}, unread: true}
	stream := noDataWiredStream(t, due, store)
	var reads []observability.Observation
	stream.coordinator.ports.Observer = observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.Stage == observability.StageNoDataMemoryRead {
			reads = append(reads, observation)
		}
	})
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(reads) != 1 || reads[0].NoDataMemoryRead == nil || !reads[0].NoDataMemoryRead.ManualClosesUnread {
		t.Fatalf("memory read lines = %+v, want one saying the closes were not read", reads)
	}
}

// fakeCloseReader answers the output check.
type fakeCloseReader struct {
	closes map[string]int64
	err    error
	asked  []string
}

func (reader *fakeCloseReader) ReadNoDataManualCloses(
	_ context.Context, _, _ string, fingerprints []string,
) (map[string]int64, error) {
	reader.asked = append(reader.asked, fingerprints...)
	return reader.closes, reader.err
}

func absentNoDataEvent(digest, fingerprint string) contract.TriggerEventV1 {
	return contract.TriggerEventV1{
		EventID: "event-" + digest, EventKind: contract.TriggerEventAbnormal, DedupeMD5: fingerprint,
		PlanRef: contract.RuntimePlanRefV1{StrategyID: "failed"},
		RecordRef: contract.TriggerRecordRefV1{DimensionIdentityDigest: digest,
			Dimensions: map[string]json.RawMessage{contract.NoDataDimensionTag: json.RawMessage("true")}},
	}
}

const (
	closedFingerprintA = "0123456789abcdef0123456789abcdef"
	closedFingerprintB = "fedcba9876543210fedcba9876543210"
	closeFirstAbsent   = int64(1000)
)

// closedOutputFixture is the output isolation fixture with its first Plan's
// two series carrying absent no-data alerts (series a on the refused series,
// b on the kept one) and the output check wired to reader.
func closedOutputFixture(t *testing.T, reader *fakeCloseReader) (*outputIsolationFixture, context.Context) {
	t.Helper()
	fixture := newOutputIsolationFixture(t)
	plan := &fixture.evaluated.Plans[0]
	plan.StateResults[0].Events = []contract.TriggerEventV1{absentNoDataEvent("series-a", closedFingerprintA)}
	plan.StateResults[1].Events = []contract.TriggerEventV1{absentNoDataEvent("series-b", closedFingerprintB)}
	fixture.coordinator.ports.NoDataManualCloses = reader
	ctx := withNoDataAbsenceStarts(context.Background(), noDataAbsenceStarts{
		plan.Plan: {"series-a": closeFirstAbsent, "series-b": closeFirstAbsent},
	})
	return fixture, ctx
}

func (fixture *outputIsolationFixture) finalizeIn(ctx context.Context) (execution.SlotExecutionResult, error) {
	return fixture.coordinator.finalizePrepared(
		ctx, fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated,
	)
}

func (fixture *outputIsolationFixture) linesAt(stage observability.Stage) []observability.Observation {
	var found []observability.Observation
	for _, observation := range fixture.observations {
		if observation.Stage == stage {
			found = append(found, observation)
		}
	}
	return found
}

func (fixture *outputIsolationFixture) sentIDs() map[string]bool {
	sent := map[string]bool{}
	for _, batch := range fixture.events.batches {
		for _, id := range batch {
			sent[id] = true
		}
	}
	return sent
}

// decision-024 section 5.2: a close recorded while the round ran keeps that
// round's alert from going out, by name, and says so on the check's own line.
func TestAnAlertClosedWhileTheRoundRanIsNotSent(t *testing.T) {
	reader := &fakeCloseReader{closes: map[string]int64{closedFingerprintA: closeFirstAbsent + 5}}
	fixture, ctx := closedOutputFixture(t, reader)
	if _, err := fixture.finalizeIn(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := fixture.sentIDs(); sent["event-series-a"] || !sent["event-series-b"] {
		t.Fatalf("sent %v, want only the alert nobody closed", sent)
	}
	checks := fixture.linesAt(observability.StageNoDataManualCloseChecked)
	if len(checks) != 1 || checks[0].NoDataManualClose == nil || checks[0].NoDataManualClose.Dropped != 1 ||
		checks[0].Trace.StrategyID != "failed" {
		t.Fatalf("check lines = %+v, want one under the Plan's strategy with the drop", checks)
	}
}

// A Plan every one of whose alerts was closed wrote nothing: there is no
// write line for it, so nothing downstream reads a write that did not happen
// as an acknowledged one. The series still move: the absence was decided.
func TestABatchTheCheckEmptiedHasNoWriteLine(t *testing.T) {
	reader := &fakeCloseReader{closes: map[string]int64{closedFingerprintA: closeFirstAbsent, closedFingerprintB: closeFirstAbsent}}
	fixture, ctx := closedOutputFixture(t, reader)
	if _, err := fixture.finalizeIn(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := fixture.sentIDs(); sent["event-series-a"] || sent["event-series-b"] {
		t.Fatalf("sent %v, want neither closed alert", sent)
	}
	for _, acked := range fixture.linesAt(observability.StageEventACKed) {
		if acked.Trace.StrategyID == "failed" {
			t.Fatalf("event_acked line %+v for a Plan that wrote nothing", acked)
		}
	}
	checks := fixture.linesAt(observability.StageNoDataManualCloseChecked)
	if len(checks) != 1 || checks[0].NoDataManualClose == nil || checks[0].NoDataManualClose.Dropped != 2 {
		t.Fatalf("check lines = %+v, want one with both drops", checks)
	}
	applied := map[execution.StateKeyIdentity]bool{}
	for _, identity := range fixture.base.stateApplied {
		applied[identity] = true
	}
	if !applied[fixture.refused] || !applied[fixture.kept] {
		t.Fatalf("applied State %v, want both closed series moved", fixture.base.stateApplied)
	}
}

// A dropped alert is not a sent one when the sink then withholds the rest of
// its series: the series sent nothing, keeps its State, and the Slot goes on
// by the rejection's name rather than failing as a sink that broke its word.
func TestADroppedAlertIsNotCountedAsSentBesideAWithheldOne(t *testing.T) {
	reader := &fakeCloseReader{closes: map[string]int64{closedFingerprintA: closeFirstAbsent}}
	fixture, ctx := closedOutputFixture(t, reader)
	plan := &fixture.evaluated.Plans[0]
	plan.StateResults[0].Events = append([]contract.TriggerEventV1{{EventID: "failed-event",
		PlanRef: contract.RuntimePlanRefV1{StrategyID: "failed"}}}, plan.StateResults[0].Events...)
	result, err := fixture.finalizeIn(ctx)
	if err != nil || result.ReasonCode != execution.ReasonCode(contract.ReasonOutputConversionRejected) {
		t.Fatalf("finalizePrepared() result=%+v error=%v, want the Slot completed by the rejection", result, err)
	}
	for _, identity := range fixture.base.stateApplied {
		if identity == fixture.refused {
			t.Fatalf("applied State %v includes the withheld series", fixture.base.stateApplied)
		}
	}
}

// statePortsRefusing refuses every State write as the ownership store does
// after a handover.
type statePortsRefusing struct {
	*planFailurePorts
}

func (*statePortsRefusing) ApplyRuntime(context.Context, execution.StateApplyRequest) (execution.StateApplyResult, error) {
	return execution.StateApplyResult{}, ownership.ErrStaleFence
}

// A State refused after a batch the check emptied left nothing at the
// consumer: no output_unapplied line counts the dropped alerts as sent.
func TestAStateRefusedAfterAnEmptiedBatchIsNotOutputUnapplied(t *testing.T) {
	reader := &fakeCloseReader{closes: map[string]int64{closedFingerprintA: closeFirstAbsent, closedFingerprintB: closeFirstAbsent}}
	fixture, ctx := closedOutputFixture(t, reader)
	fixture.coordinator.ports.State = &statePortsRefusing{planFailurePorts: fixture.ports}
	if _, err := fixture.finalizeIn(ctx); !errors.Is(err, ownership.ErrStaleFence) {
		t.Fatalf("finalizePrepared() error = %v, want the refusal", err)
	}
	if unapplied := fixture.linesAt(observability.StageOutputUnapplied); len(unapplied) != 0 {
		t.Fatalf("output_unapplied lines = %+v, want none: nothing was sent", unapplied)
	}
}

// A failed read lets the batch out, and the check's line says it could not
// check; an absence that began after the close is sent.
func TestTheOutputCheckFailsOpen(t *testing.T) {
	t.Run("a failed read", func(t *testing.T) {
		fixture, ctx := closedOutputFixture(t, &fakeCloseReader{err: errors.New("redis down")})
		if _, err := fixture.finalizeIn(ctx); err != nil {
			t.Fatal(err)
		}
		if sent := fixture.sentIDs(); !sent["event-series-a"] || !sent["event-series-b"] {
			t.Fatalf("sent %v, want the batch out unchecked", sent)
		}
		checks := fixture.linesAt(observability.StageNoDataManualCloseChecked)
		if len(checks) != 1 || checks[0].NoDataManualClose == nil || !checks[0].NoDataManualClose.CheckFailed ||
			checks[0].Result != observability.ResultFailed || checks[0].Err == nil ||
			string(checks[0].ReasonCode) != contract.ReasonRedisUnavailable {
			t.Fatalf("check lines = %+v, want one failed line with its cause saying the check failed", checks)
		}
	})
	t.Run("an absence that began after the close", func(t *testing.T) {
		fixture, ctx := closedOutputFixture(t, &fakeCloseReader{closes: map[string]int64{closedFingerprintA: closeFirstAbsent - 1}})
		if _, err := fixture.finalizeIn(ctx); err != nil {
			t.Fatal(err)
		}
		if sent := fixture.sentIDs(); !sent["event-series-a"] {
			t.Fatalf("sent %v, want a later absence's alert sent", sent)
		}
		if checks := fixture.linesAt(observability.StageNoDataManualCloseChecked); len(checks) != 0 {
			t.Fatalf("check lines = %+v, want none for a batch the check left alone", checks)
		}
	})
}
