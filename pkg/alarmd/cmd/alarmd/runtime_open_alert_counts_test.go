// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// The copy's counts become the snapshot's by strategy id, sorted, zeros left
// out; one id under two tenants takes the larger count, and an id unknown
// under one tenant stays unknown though another counts it.
func TestOpenAlertCountsAreTheCopysByStrategy(t *testing.T) {
	counts := map[openalerts.StrategyKey]int{
		{TenantID: "system", StrategyID: "12"}: 3,
		{TenantID: "other", StrategyID: "12"}:  5,
		{TenantID: "system", StrategyID: "7"}:  1,
		{TenantID: "system", StrategyID: "8"}:  0,
	}
	unknown := []openalerts.StrategyKey{{TenantID: "system", StrategyID: "9"}, {TenantID: "other", StrategyID: "7"}}
	got := openAlertCountsOf(counts, unknown, []string{"12", "7", "8", "9"})
	want := &fleet.OpenAlertCounts{Counts: []fleet.StrategyOpenCount{{Strategy: "12", Count: 5}, {Strategy: "7", Count: 1}}, Unknown: []string{"7", "9"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
}

// A strategy of an owned object the copy does not track -- refused for its
// capacity, or not taken up yet after a takeover -- is unknown, never zero;
// owned strategies that cannot be named say so.
func TestOpenAlertCountsNameUntrackedOwnedStrategiesUnknown(t *testing.T) {
	counts := map[openalerts.StrategyKey]int{{TenantID: "system", StrategyID: "1"}: 0}
	got := openAlertCountsOf(counts, nil, []string{"1", "2"})
	if !reflect.DeepEqual(got.Unknown, []string{"2"}) || len(got.Counts) != 0 || got.OwnedUnnamed {
		t.Fatalf("untracked owned strategy: %+v, want 2 unknown", got)
	}
	if got := openAlertCountsOf(counts, nil, nil); !got.OwnedUnnamed {
		t.Fatalf("unnamed owned strategies: %+v, want said unnamed", got)
	}
	if got := openAlertCountsOf(counts, nil, []string{}); got.OwnedUnnamed || len(got.Unknown) != 0 {
		t.Fatalf("nothing owned: %+v, want nothing unknown", got)
	}
}

// The publisher hands the copy its owned objects' strategies, and an empty
// list rather than nil when it owns nothing.
func TestFleetPublisherNamesOwnedStrategiesForOpenAlertCounts(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	var given []string
	publisher := fleetPublisher{tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-a", "qg-b"} },
		strategies: func(queryGroup string) []fleet.StrategyRef {
			return map[string][]fleet.StrategyRef{"qg-a": {{StrategyID: "3"}}, "qg-b": {{StrategyID: "3"}, {StrategyID: "1"}}}[queryGroup]
		},
		openAlertCounts: func(owned []string) *fleet.OpenAlertCounts { given = owned; return &fleet.OpenAlertCounts{} }}
	publisher.snapshot(context.Background())
	if !reflect.DeepEqual(given, []string{"1", "3"}) {
		t.Fatalf("owned strategies given %v, want 1 and 3", given)
	}
	publisher.owned = func() []execution.QueryGroupIdentity { return nil }
	publisher.snapshot(context.Background())
	if given == nil || len(given) != 0 {
		t.Fatalf("owning nothing gave %#v, want an empty list", given)
	}
	publisher.strategies = nil
	publisher.snapshot(context.Background())
	if given != nil {
		t.Fatalf("without strategies gave %#v, want nil", given)
	}
}

// The publisher carries the counts it is given and none when it has no copy.
func TestFleetPublisherCarriesOpenAlertCounts(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	counts := &fleet.OpenAlertCounts{Counts: []fleet.StrategyOpenCount{{Strategy: "1", Count: 2}}}
	publisher := fleetPublisher{tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned:           func() []execution.QueryGroupIdentity { return nil },
		openAlertCounts: func([]string) *fleet.OpenAlertCounts { return counts }}
	if snapshot := publisher.snapshot(context.Background()); !reflect.DeepEqual(snapshot.OpenAlerts, counts) {
		t.Fatalf("open alerts = %+v, want %+v", snapshot.OpenAlerts, counts)
	}
	publisher.openAlertCounts = nil
	if snapshot := publisher.snapshot(context.Background()); snapshot.OpenAlerts != nil {
		t.Fatalf("a publisher without a copy published %+v", snapshot.OpenAlerts)
	}
}
