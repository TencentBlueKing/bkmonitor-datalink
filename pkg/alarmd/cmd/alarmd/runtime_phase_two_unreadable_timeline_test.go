// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// scriptedRounds answers each round with the next result of its script and
// holds the last one once the script runs out.
func scriptedRounds(script ...execution.SlotExecutionResult) *callbackPhaseTwoQueryGroup {
	var calls atomic.Int32
	return &callbackPhaseTwoQueryGroup{run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
		index := int(calls.Add(1)) - 1
		if index >= len(script) {
			index = len(script) - 1
		}
		return script[index], true, nil
	}}
}

func (owner *fakePhaseTwoOwnership) lastRegistration(t *testing.T) ownership.WorkerRegistration {
	t.Helper()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if len(owner.registrations) == 0 {
		t.Fatal("no registration was written")
	}
	return owner.registrations[len(owner.registrations)-1]
}

// The words a Slot source refusal reports, as the Runner returns them.
var (
	unreadableRound = execution.SlotExecutionResult{Result: observability.ResultRetrying,
		ReasonCode: "SCHEDULE_UNREADABLE", SourceRetry: true}
	viewRefusedRound = execution.SlotExecutionResult{Result: observability.ResultRetrying,
		ReasonCode: "VIEW_NOT_EXECUTABLE", SourceRetry: true}
	ranRound = execution.SlotExecutionResult{Result: observability.ResultSuccess, Completed: true}
)

// A round that meets a timeline that does not decode used to end there: the
// Worker named it SCHEDULE_UNREADABLE in its own line and told nobody, and the
// Query Group ran nothing for as long as the key lived. Now the replica's
// registration names the Query Group, so a leader reads the timeline again;
// a round the source refused for another reason - one that says nothing
// about the bytes - leaves the name, and the first later round that runs a
// Slot drops it.
func TestARoundThatMeetsAnUnreadableTimelineNamesItOnTheRegistration(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	broken := execution.QueryGroupIdentity("query-group-unreadable")
	healthy := execution.QueryGroupIdentity("query-group-healthy")
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{broken, healthy}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{broken, healthy},
		runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
			broken:  scriptedRounds(unreadableRound, viewRefusedRound, ranRound),
			healthy: scriptedRounds(ranRound),
		}}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	if registration := owner.lastRegistration(t); len(registration.UnreadableTimelines) != 0 || registration.UnreadableTimelinesTotal != 0 {
		t.Fatalf("a replica that ran nothing yet names %v (%d)", registration.UnreadableTimelines, registration.UnreadableTimelinesTotal)
	}
	round := func(want []string, step string) {
		t.Helper()
		if err := runScheduledOnceSettled(context.Background(), bundle); err != nil {
			t.Fatal(err)
		}
		if err := bundle.register(context.Background(), ownership.WorkerReady); err != nil {
			t.Fatal(err)
		}
		registration := owner.lastRegistration(t)
		if !reflect.DeepEqual(registration.UnreadableTimelines, want) || registration.UnreadableTimelinesTotal != len(want) {
			t.Fatalf("after %s the registration names %v (total %d), want %v", step, registration.UnreadableTimelines,
				registration.UnreadableTimelinesTotal, want)
		}
	}
	round([]string{string(broken)}, "the round that met the unreadable timeline")
	round([]string{string(broken)}, "a round the view refused, which read no timeline")
	round(nil, "the round that ran a Slot")
}

// The registration carries at most MaxReportedUnreadableTimelines names, the
// first in Query Group order, and says how many there are in all: a decode
// failure across a whole replica must not put every owned Query Group on
// every heartbeat. A Query Group that leaves the replica leaves the names.
func TestTheUnreadableTimelineNamesAreBounded(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	owned := make([]execution.QueryGroupIdentity, 0, ownership.MaxReportedUnreadableTimelines+3)
	runners := make(map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime)
	for index := 0; index < ownership.MaxReportedUnreadableTimelines+3; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("query-group-%03d", index))
		owned = append(owned, queryGroup)
		runners[queryGroup] = scriptedRounds(unreadableRound)
	}
	control := &fakePhaseTwoControl{queryGroups: owned}
	owner := &fakePhaseTwoOwnership{assigned: owned, runners: runners}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	if err := runScheduledOnceSettled(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	names, total := bundle.unreadableTimelinesRegistration()
	if total != len(owned) || len(names) != ownership.MaxReportedUnreadableTimelines ||
		names[0] != string(owned[0]) || names[len(names)-1] != string(owned[ownership.MaxReportedUnreadableTimelines-1]) {
		t.Fatalf("names %v, total %d; want the first %d in order of %d", names, total,
			ownership.MaxReportedUnreadableTimelines, len(owned))
	}
	bundle.mu.Lock()
	bundle.removeRunnerLocked(owned[0])
	bundle.mu.Unlock()
	if names, total := bundle.unreadableTimelinesRegistration(); total != len(owned)-1 || len(names) == 0 || names[0] != string(owned[1]) {
		t.Fatalf("after the first Query Group left: names %v, total %d; want it gone", names, total)
	}
}

// The leader's round hands the activation reconciler the names its ready
// Workers carry, among the Query Groups it runs, and nothing else; the next
// round's names replace them whole.
func TestTheLeaderRoundCollectsTheNamedTimelines(t *testing.T) {
	requests := &controlplane.TimelineRepairRequests{}
	runtime := &productionPhaseTwoOwnership{dependencies: productionPhaseTwoOwnershipDependencies{TimelineRepairs: requests}}
	workers := []ownership.WorkerRegistration{
		{WorkerID: "w1", UnreadableTimelines: []string{"query-group-b", "query-group-gone"}},
		{WorkerID: "w2", UnreadableTimelines: []string{"query-group-a", "query-group-b"}},
		{WorkerID: "w3"},
	}
	active := []execution.QueryGroupIdentity{"query-group-a", "query-group-b", "query-group-c"}
	runtime.collectUnreadableTimelines(workers, active)
	if got := requests.UnreadableTimelines(); !reflect.DeepEqual(got, []execution.QueryGroupIdentity{"query-group-a", "query-group-b"}) {
		t.Fatalf("collected %v, want the two running Query Groups named, once each", got)
	}
	runtime.collectUnreadableTimelines([]ownership.WorkerRegistration{{WorkerID: "w1"}}, active)
	if got := requests.UnreadableTimelines(); len(got) != 0 {
		t.Fatalf("collected %v after no Worker named anything, want none", got)
	}
}
