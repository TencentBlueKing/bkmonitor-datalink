// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// The activation body is lost under its header while one Query Group's
// timeline is gone or does not decode, and the source keeps publishing the
// same content. The Leader's rebuild is refused every round, by name, and so
// is every activation round; nothing renews what the activation names, so
// when those keys expire the fleet stops detecting. No newer publication
// waits, so the fleet is not behind and ACTIVATION_BEHIND cannot see it.
// Through the production wiring against Redis: the refusal is counted under
// its outcome, one failed round inside the bound leaves the fleet as it was,
// and once the failing has lasted past the bound - long before the catalog
// expires - the fleet is DEGRADED with ACTIVATION_FAILING.
func TestAnActivationRoundFailingPastTheBoundDegradesTheFleetWithNoNewerPublication(t *testing.T) {
	for _, tc := range []struct {
		name    string
		spoil   func(t *testing.T, fixture *lifecycleFixture, key string)
		outcome controlplane.ActivationRebuildOutcome
	}{
		{name: "absent", outcome: controlplane.ActivationRebuildTimelineMissing,
			spoil: func(t *testing.T, fixture *lifecycleFixture, key string) {
				if err := fixture.redis.Del(context.Background(), key).Err(); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "undecodable", outcome: controlplane.ActivationRebuildTimelineUnreadable,
			spoil: func(t *testing.T, fixture *lifecycleFixture, key string) {
				if err := fixture.redis.Set(context.Background(), key, "{not a timeline", time.Hour).Err(); err != nil {
					t.Fatal(err)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, noDataOff: true,
				targets: []string{lifecycleHostA, lifecycleHostB}}, nil)
			ctx := context.Background()
			repository := fixture.bundle.dependencies.Control.(*productionPhaseTwoControl).dependencies.Repository.(*controlplane.RedisCatalogRepository)
			prefix := productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "catalog")
			if err := fixture.redis.Del(ctx, prefix+":activation").Err(); err != nil {
				t.Fatal(err)
			}
			tc.spoil(t, fixture, prefix+":schedule_timeline:"+string(fixture.queryGroup))

			start := time.UnixMilli(fixture.clock.Load())
			round := func(at time.Time) *fleet.ActivationFacts {
				t.Helper()
				fixture.clock.Store(at.UnixMilli())
				// The round's own error is what the standing records; the
				// case reads the standing.
				_ = fixture.bundle.refreshAndReconcile(ctx, true)
				return fixture.bundle.activationFleetFacts()
			}
			verdict := func(facts *fleet.ActivationFacts, at time.Time) fleet.View {
				return fleet.Aggregate(fleet.Expectation{Known: true, QueryGroups: 1},
					[]fleet.Snapshot{{Replica: fixture.workerID, TakenAt: at, Owned: 1, Determined: 1, Activation: facts}},
					[]string{fixture.workerID}, at, time.Hour)
			}
			failing := func(view fleet.View) *fleet.Degradation {
				for index := range view.Degradations {
					if view.Degradations[index].Kind == fleet.DegradationActivationFailing {
						return &view.Degradations[index]
					}
				}
				return nil
			}

			// One failed round: refused by name, counted, and inside the bound.
			first := start.Add(time.Minute)
			facts := round(first)
			if got := repository.ActivationRebuildCounts()[tc.outcome]; got < 1 {
				t.Fatalf("rebuild counts %v, want the refusal under %s", repository.ActivationRebuildCounts(), tc.outcome)
			}
			if facts == nil || facts.ConsecutiveFailures < 1 || facts.Behind || facts.FailingBeyondBound {
				t.Fatalf("facts after one failed round = %+v, want failing, not behind, not beyond the bound", facts)
			}
			if view := verdict(facts, first); failing(view) != nil || view.Health != fleet.HealthHealthy {
				t.Fatalf("one failed round inside the bound: health %s degradations %+v, want HEALTHY and no ACTIVATION_FAILING",
					view.Health, view.Degradations)
			}

			// Past the bound, still failing, still nothing newer published.
			past := first.Add(controlplane.SourceStalenessBound + time.Minute)
			if past.Sub(start) >= fixture.cfg.PhaseTwo.Control.CatalogTTL.Duration() {
				t.Fatalf("setup: %v past the start is not before the catalog TTL %v", past.Sub(start), fixture.cfg.PhaseTwo.Control.CatalogTTL)
			}
			facts = round(past)
			if facts == nil || facts.Behind || !facts.FailingBeyondBound || facts.ConsecutiveFailures < 2 {
				t.Fatalf("facts past the bound = %+v, want failing beyond the bound and not behind", facts)
			}
			view := verdict(facts, past)
			degradation := failing(view)
			if degradation == nil || view.Health != fleet.HealthDegraded || degradation.Replica != fixture.workerID ||
				degradation.Stage != facts.FailureStage || degradation.Text == "" || degradation.AgeSeconds == nil ||
				*degradation.AgeSeconds <= controlplane.SourceStalenessBound.Seconds() {
				t.Fatalf("past the bound: health %s degradations %+v, want DEGRADED with ACTIVATION_FAILING naming the failure",
					view.Health, view.Degradations)
			}
		})
	}
}
