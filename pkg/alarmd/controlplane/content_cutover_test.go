// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// TestOutputContextRevisionFloorFollowsTheClientWriteBound: the floor is
// the write bound the client's timeout and retries give, plus the clock
// allowance, so a changed timeout or retry count moves it; a client without
// options gets the go-redis defaults.
func TestOutputContextRevisionFloorFollowsTheClientWriteBound(t *testing.T) {
	cases := []struct {
		name    string
		options *redis.Options
		want    time.Duration
	}{
		{name: "defaults", options: &redis.Options{}, want: 3*time.Second*4 + contentCutoverClockAllowance},
		{name: "longer write timeout", options: &redis.Options{WriteTimeout: 5 * time.Second}, want: 5*time.Second*4 + contentCutoverClockAllowance},
		{name: "more retries", options: &redis.Options{WriteTimeout: 2 * time.Second, MaxRetries: 5}, want: 2*time.Second*6 + contentCutoverClockAllowance},
		{name: "retries disabled", options: &redis.Options{WriteTimeout: 2 * time.Second, MaxRetries: -1}, want: 2*time.Second + contentCutoverClockAllowance},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &RedisCatalogRepository{client: redis.NewClient(testCase.options)}
			if got := repository.outputContextRevisionFloor(); got != testCase.want {
				t.Fatalf("floor=%s want %s", got, testCase.want)
			}
		})
	}
	repository := &RedisCatalogRepository{client: redis.NewClient(&redis.Options{})}
	segment := execution.ScheduleSegmentFact{Start: 1000}
	if since := repository.outputContextRevisionSince(segment, 1050); since != 1080 {
		t.Fatalf("a revision is placed the floor past the boundary: since=%d want 1080", since)
	}
	segment.OutputContextRevisions = []execution.OutputContextRevision{{Since: 1100}}
	if since := repository.outputContextRevisionSince(segment, 1050); since != 1101 {
		t.Fatalf("a revision is placed past the last one already on the Segment: since=%d want 1101", since)
	}
}

// TestPruneOutputContextRevisionsFoldsOnlyWhatNoSlotResolves: a revision is
// folded into the base once every Slot before its Since is past the
// keep-until bound, and never before.
func TestPruneOutputContextRevisionsFoldsOnlyWhatNoSlotResolves(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1"}
	refs := func(digest execution.OutputContextDigest) []execution.OutputContextRef {
		return []execution.OutputContextRef{{Plan: plan, Digest: digest}}
	}
	retention := execution.SlotRetention{QueryReserve: 10 * time.Second, MaxReplayAge: 10 * time.Minute, TerminalDelay: 5 * time.Minute}
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}
	planRevision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	plans := []execution.FrozenPlanSchedule{{Identity: plan, ScheduleRevision: planRevision, Spec: spec}}
	scheduleRevision, err := execution.DeriveQueryGroupScheduleRevision(plans)
	if err != nil {
		t.Fatal(err)
	}
	schedule := execution.FrozenQueryGroupSchedule{
		Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: "rev", PublicationEpoch: 1},
			QueryGroup:  "qg", QueryRevision: "q", ScheduleRevision: scheduleRevision, Start: 1000,
			ObjectDigest: "obj", OutputContextRefs: refs("ctx-0"),
			OutputContextRevisions: []execution.OutputContextRevision{{Since: 2000, Refs: refs("ctx-1")}, {Since: 3000, Refs: refs("ctx-2")}},
		},
		Plans: plans,
	}
	keep := int64(15*60 + 30)
	if folded := pruneOutputContextRevisions(&schedule, retention, time.Unix(2000+keep, 0)); folded != 0 || len(schedule.Segment.OutputContextRevisions) != 2 {
		t.Fatalf("a revision whose Slots are still within retention must be kept: folded=%d revisions=%+v", folded, schedule.Segment.OutputContextRevisions)
	}
	if folded := pruneOutputContextRevisions(&schedule, retention, time.Unix(2000+keep+1, 0)); folded != 1 ||
		len(schedule.Segment.OutputContextRevisions) != 1 || schedule.Segment.OutputContextRefs[0].Digest != "ctx-1" {
		t.Fatalf("the first revision folds into the base once its Slots are past retention: folded=%d segment=%+v", folded, schedule.Segment)
	}
	if err := schedule.Validate(); err != nil {
		t.Fatal(err)
	}
	if folded := pruneOutputContextRevisions(&schedule, retention, time.Unix(3000+keep+1, 0)); folded != 1 ||
		schedule.Segment.OutputContextRevisions != nil || schedule.Segment.OutputContextRefs[0].Digest != "ctx-2" {
		t.Fatalf("the last revision folds into the base and the history empties: folded=%d segment=%+v", folded, schedule.Segment)
	}
}
