// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategycache"
)

// A replica's snapshot carries what its table dropped once it has dropped
// anything, and omits it before. The round that overflows the default table
// arrives before the first publish that would raise the bound.
func TestTheSnapshotCarriesWhatTheTableDropped(t *testing.T) {
	now := func() time.Time { return time.Unix(20_000, 0) }
	tracker := fleet.NewTracker(nil, "replica-1", now)
	var owned []execution.QueryGroupIdentity
	publisher := &fleetPublisher{tracker: tracker, replica: "replica-1", now: now,
		owned: func() []execution.QueryGroupIdentity { return owned }}
	round := func(queryGroup string) {
		tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: "FULL_COMPLETED",
			Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: "1", BusinessID: "2"}})
	}
	if encoded, _ := json.Marshal(publisher.snapshot(context.Background())); strings.Contains(string(encoded), "tracker_dropped") {
		t.Fatalf("a table that lost nothing: %s", encoded)
	}
	for index := 0; index < fleet.DefaultTrackedQueryGroups; index++ {
		round(fmt.Sprintf("qg-%d", index))
		owned = append(owned, execution.QueryGroupIdentity(fmt.Sprintf("qg-%d", index)))
	}
	round("qg-over")
	snapshot := publisher.snapshot(context.Background())
	if snapshot.TrackerDropped == nil || snapshot.TrackerDropped.Counts[fleet.TrackerDroppedObservation] != 1 ||
		fmt.Sprint(snapshot.TrackerDropped.Recent) != "[qg-over]" {
		t.Fatalf("dropped = %+v, want qg-over's round", snapshot.TrackerDropped)
	}
}

// A replica's snapshot carries the effective-time dependencies its cache
// could not read, with their total.
func TestTheSnapshotCarriesTheUnreadEffectiveTimeDependencies(t *testing.T) {
	now := func() time.Time { return time.Unix(20_000, 0) }
	unread := []strategycache.EffectiveTimeUnread{{Tenant: "system", Business: "2", Class: "read", FailingSince: now()}}
	publisher := &fleetPublisher{tracker: fleet.NewTracker(nil, "replica-1", now), replica: "replica-1", now: now,
		owned:               func() []execution.QueryGroupIdentity { return nil },
		effectiveTimeUnread: func() ([]strategycache.EffectiveTimeUnread, int) { return unread, 3 }}
	snapshot := publisher.snapshot(context.Background())
	if fmt.Sprint(snapshot.EffectiveTimeUnread) != fmt.Sprint(unread) || snapshot.EffectiveTimeUnreadTotal != 3 {
		t.Fatalf("snapshot %+v (%d)", snapshot.EffectiveTimeUnread, snapshot.EffectiveTimeUnreadTotal)
	}
}
