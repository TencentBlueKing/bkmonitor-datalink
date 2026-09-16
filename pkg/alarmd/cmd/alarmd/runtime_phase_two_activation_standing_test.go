// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The shape a running deployment was in for half a day: the source publishes
// every round, the activation fails to follow it every round, and the fleet
// executes the last good publication. The source clock reads fresh
// throughout. This standing is the one that sees it: behind from the first
// failure, past the bound after six minutes of it, back to level on the
// first success.
func TestActivationStandingOnALeaderWhoseCutoverFails(t *testing.T) {
	lastGood := controlplane.SnapshotPublicationRef{SnapshotRevision: "bdc6ffcb0000", PublicationEpoch: 7}
	published := controlplane.SnapshotPublicationRef{SnapshotRevision: "e7a1b2c30000", PublicationEpoch: 8}
	failed := func() phaseTwoControlRefreshResult {
		cause := &controlplane.ActivationFailureError{
			Failure: controlplane.ActivationFailure{
				Stage: controlplane.ActivationFailureStageScheduleCutover,
				Class: controlplane.ActivationFailureClassScheduleConflict,
			},
			Err: controlplane.ErrScheduleConflict,
		}
		result := degradedBy(cause)
		result.SourceKind = observability.SourceKindCompiledSnapshot
		result.Activation = &phaseTwoActivationOutcome{
			Published: published, Applied: lastGood,
			Failure: &cause.Failure, Cause: cause,
		}
		return result
	}
	// A round that succeeded before any of it: the standing starts level.
	level := healthyResult()
	level.Activation = &phaseTwoActivationOutcome{Published: lastGood, Applied: lastGood}
	recovered := healthyResult()
	recovered.Activation = &phaseTwoActivationOutcome{Published: published, Applied: published}
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"},
		initialResult: level, refreshResults: []phaseTwoControlRefreshResult{failed(), failed(), failed(), recovered}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	fixture := newControlSourceFixture(t, control, owner)
	if facts := fixture.bundle.activationFleetFacts(); facts != nil {
		t.Fatalf("facts before any attempt = %+v, want none: a follower never attempts and must not read as level", facts)
	}
	fixture.start()

	facts := fixture.bundle.activationFleetFacts()
	if facts == nil || facts.Behind || facts.BehindBeyondBound || facts.ConsecutiveFailures != 0 ||
		facts.Applied != string(lastGood.SnapshotRevision) || facts.Published != string(lastGood.SnapshotRevision) ||
		facts.LastSuccessAgeSeconds == nil || facts.FailingSecondsThisProcess != nil {
		t.Fatalf("facts after a level round = %+v", facts)
	}

	// First failure: behind at once, not yet beyond the bound; the fleet is
	// named as executing the last good publication while the source is on
	// the new one, and the classification is the log line's.
	fixture.clock = fixture.clock.Add(time.Minute)
	fixture.tick()
	facts = fixture.bundle.activationFleetFacts()
	if facts == nil || !facts.Behind || facts.BehindBeyondBound || facts.ConsecutiveFailures != 1 ||
		facts.Applied != string(lastGood.SnapshotRevision) || facts.Published != string(published.SnapshotRevision) ||
		facts.FailureStage != "schedule_cutover" || facts.FailureClass != "schedule_conflict" ||
		!strings.Contains(facts.LastFailure, "schedule activation conflict") ||
		facts.FailingSecondsThisProcess == nil || *facts.FailingSecondsThisProcess != 0 {
		t.Fatalf("facts after the first failure = %+v", facts)
	}
	if facts.Reason() != "schedule_cutover/schedule_conflict" {
		t.Fatalf("reason = %q, want stage/class", facts.Reason())
	}
	// Two more inside the bound: the run lengthens, the start does not move.
	fixture.clock = fixture.clock.Add(time.Minute)
	fixture.tick()
	fixture.clock = fixture.clock.Add(time.Minute)
	fixture.tick()
	facts = fixture.bundle.activationFleetFacts()
	if facts.ConsecutiveFailures != 3 || *facts.FailingSecondsThisProcess != 120 || facts.BehindBeyondBound {
		t.Fatalf("facts three failures in = %+v, want 3 consecutive, failing 120 s, not yet beyond the bound", facts)
	}
	// Past the bound, with no round in between: beyond, on this process's
	// clock, which is the only one there is for it.
	fixture.clock = fixture.clock.Add(controlplane.SourceStalenessBound)
	if facts := fixture.bundle.activationFleetFacts(); !facts.BehindBeyondBound || !facts.Behind {
		t.Fatalf("facts past the bound = %+v, want behind beyond bound", facts)
	}
	// A success: level again, and every failure fact cleared with it.
	fixture.tick()
	facts = fixture.bundle.activationFleetFacts()
	if facts.Behind || facts.BehindBeyondBound || facts.ConsecutiveFailures != 0 || facts.FailingSecondsThisProcess != nil ||
		facts.FailureStage != "" || facts.LastFailure != "" ||
		facts.Applied != string(published.SnapshotRevision) || facts.Published != string(published.SnapshotRevision) ||
		facts.LastSuccessAgeSeconds == nil || *facts.LastSuccessAgeSeconds != 0 {
		t.Fatalf("facts after recovery = %+v, want level with nothing failing", facts)
	}
}

// A failure whose last good activation could not even be read leaves the
// previous applied publication standing: "unknown now" is not evidence the
// fleet stopped executing what it was executing.
func TestActivationStandingKeepsAppliedWhenTheLastGoodCannotBeRead(t *testing.T) {
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}}
	lastGood := controlplane.SnapshotPublicationRef{SnapshotRevision: "bdc6ffcb0000", PublicationEpoch: 7}
	published := controlplane.SnapshotPublicationRef{SnapshotRevision: "e7a1b2c30000", PublicationEpoch: 8}
	bundle.noteActivationLocked(&phaseTwoActivationOutcome{Published: lastGood, Applied: lastGood})
	bundle.noteActivationLocked(&phaseTwoActivationOutcome{Published: published, Cause: controlplane.ErrActivationUnavailable})
	facts := bundle.activationFleetFacts()
	if facts == nil || facts.Applied != string(lastGood.SnapshotRevision) || !facts.Behind || facts.ConsecutiveFailures != 1 ||
		facts.FailureStage != "" || facts.Reason() != "unclassified" {
		t.Fatalf("facts = %+v, want applied kept at the last good, behind, one failure, unclassified", facts)
	}
	// A round that did not attempt activation leaves the standing alone.
	bundle.noteActivationLocked(nil)
	if again := bundle.activationFleetFacts(); again.ConsecutiveFailures != 1 {
		t.Fatalf("a round without an attempt changed the standing: %+v", again)
	}
}

// The publisher puts the standing on the snapshot, and publishes none when
// this replica has none to give -- a follower must not read as level.
func TestFleetPublisherCarriesTheActivationStanding(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	facts := &fleet.ActivationFacts{Applied: "bdc6ffcb", Published: "e7a1b2c3", Behind: true, ConsecutiveFailures: 2}
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned:      func() []execution.QueryGroupIdentity { return nil },
		activation: func() *fleet.ActivationFacts { return facts },
	}
	if snapshot := publisher.snapshot(context.Background()); snapshot.Activation == nil || *snapshot.Activation != *facts {
		t.Fatalf("snapshot activation = %+v, want the standing as given", snapshot.Activation)
	}
	publisher.activation = func() *fleet.ActivationFacts { return nil }
	if snapshot := publisher.snapshot(context.Background()); snapshot.Activation != nil {
		t.Fatalf("a replica with no standing published %+v", snapshot.Activation)
	}
}

// The publisher puts the leader's rebalance round on the snapshot the same
// way, and none when this replica planned none: a follower must not read as
// a leader whose round moves nothing.
func TestFleetPublisherCarriesTheRebalanceRound(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	facts := &fleet.RebalanceFacts{PlannedAt: clock.now(), ReadyWorkers: 2, Assigned: 2370, Target: 1185, MostOwned: 2370,
		MostOwnedBy: "replica-1", LeastOwnedBy: "replica-2", Batch: 23, PlannedMoves: 23, StopSpreadPercent: 5, Shadow: true}
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned:     func() []execution.QueryGroupIdentity { return nil },
		rebalance: func() *fleet.RebalanceFacts { return facts },
	}
	if snapshot := publisher.snapshot(context.Background()); snapshot.Rebalance == nil || *snapshot.Rebalance != *facts {
		t.Fatalf("snapshot rebalance = %+v, want the round as given", snapshot.Rebalance)
	}
	publisher.rebalance = func() *fleet.RebalanceFacts { return nil }
	if snapshot := publisher.snapshot(context.Background()); snapshot.Rebalance != nil {
		t.Fatalf("a replica with no round published %+v", snapshot.Rebalance)
	}
}

// The bundle reads the round from an ownership runtime that plans one and
// nothing from one that does not: the production runtime is the source, and
// a fake without the method is a deployment with no round, not a nil
// dereference.
func TestBundleReadsTheRebalanceRoundOnlyFromARuntimeThatPlans(t *testing.T) {
	facts := &fleet.RebalanceFacts{PlannedMoves: 1, MostOwnedBy: "a", LeastOwnedBy: "b", Shadow: true}
	planning := &planningOwnershipRuntime{fakePhaseTwoOwnership: &fakePhaseTwoOwnership{}, last: facts}
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Ownership: planning}}
	if got := bundle.rebalanceFleetFacts(); got == nil || *got != *facts {
		t.Fatalf("rebalanceFleetFacts() = %+v, want the runtime's round", got)
	}
	bundle.dependencies.Ownership = &fakePhaseTwoOwnership{}
	if got := bundle.rebalanceFleetFacts(); got != nil {
		t.Fatalf("rebalanceFleetFacts() from a runtime that does not plan = %+v, want nil", got)
	}
	if got := (*phaseTwoWorkerBundle)(nil).rebalanceFleetFacts(); got != nil {
		t.Fatalf("rebalanceFleetFacts() on a nil bundle = %+v, want nil", got)
	}
}

type planningOwnershipRuntime struct {
	*fakePhaseTwoOwnership
	last *fleet.RebalanceFacts
}

func (runtime *planningOwnershipRuntime) LastRebalance() *fleet.RebalanceFacts { return runtime.last }

// A retained record of past loss carries the strategies behind the object,
// so the row built from it can be traced to something a reader can act on.
// Without them the record rendered as a row with an empty strategy column.
func TestFleetPublisherNamesTheStrategiesOnRetainedRecords(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	tracker := fleet.NewTracker(nil, "replica-1", clock.now)
	ctx := observability.ContextWithTraceFields(context.Background(),
		observability.TraceFields{QueryGroupKey: "qg-skip", EvaluationTime: 100, StrategyID: "1854", BusinessID: "7"})
	tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "GAP_SKIPPED",
		Trace: observability.TraceFields{QueryGroupKey: "qg-skip", StrategyID: "1854", BusinessID: "7"}})
	publisher := fleetPublisher{
		tracker: tracker, replica: "replica-1", now: clock.now,
		owned:      func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-skip"} },
		strategies: tracker.StrategiesFor,
	}
	snapshot := publisher.snapshot(context.Background())
	skip, retained := snapshot.GapSkips["qg-skip"]
	if !retained {
		t.Fatalf("snapshot gap skips = %+v, want qg-skip retained", snapshot.GapSkips)
	}
	if len(skip.Strategies) != 1 || skip.Strategies[0].StrategyID != "1854" || skip.Strategies[0].BusinessID != "7" {
		t.Fatalf("retained record strategies = %+v, want strategy 1854 of business 7", skip.Strategies)
	}
}
