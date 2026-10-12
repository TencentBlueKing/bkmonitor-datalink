// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// retentionFacts is a day's catalog and a week's object limit, with inputs
// that are not the lengths themselves.
func retentionFacts(objectLimitSeconds int64) *observability.RuntimeRetentionFacts {
	return &observability.RuntimeRetentionFacts{CatalogSeconds: 86400, ObjectLimitSeconds: objectLimitSeconds,
		SnapshotMinimumSeconds: 86100, CatalogKeyCadenceSeconds: 86400, CatalogTTLSeconds: 3600,
		RedisMaxTTLSeconds: objectLimitSeconds, RedisRestartMarginSeconds: 600, DownstreamExecutionReserveSeconds: 30,
		PublicationDelayAllowanceSeconds: 90, MaxReplayAgeSeconds: 120, PostRecoveryTerminalDelaySeconds: 60}
}

// The view groups the replicas by the retention facts they published, the
// way it groups builds: one group is a deployment that agrees with itself,
// two a rollout or a values file that changed under one replica, and a
// replica that published none is its own group without facts rather than
// filled in with another's lengths.
func TestTheViewGroupsTheReplicasByTheRetentionTheyRunWith(t *testing.T) {
	agreeing := idleSnapshots()
	agreeing[0].Retention, agreeing[1].Retention = retentionFacts(604800), retentionFacts(604800)
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, agreeing, replicas(), now, freshness)
	if len(view.Retentions) != 1 || len(view.Retentions[0].Replicas) != 2 || view.Retentions[0].Retention == nil ||
		*view.Retentions[0].Retention != *retentionFacts(604800) {
		t.Fatalf("retention groups = %+v, want one group of both replicas with the week's object limit", view.Retentions)
	}
	// The group holds a copy: a later change to the snapshot's facts does
	// not reach it.
	agreeing[0].Retention.ObjectLimitSeconds = 1
	if view.Retentions[0].Retention.ObjectLimitSeconds != 604800 {
		t.Fatal("the retention group aliases a snapshot's facts")
	}

	split := idleSnapshots()
	split[0].Retention, split[1].Retention = retentionFacts(604800), retentionFacts(86400)
	view = Aggregate(Expectation{QueryGroups: 0, Known: true}, split, replicas(), now, freshness)
	if len(view.Retentions) != 2 {
		t.Fatalf("retention groups = %+v, want two: the replicas disagree and no single set is made of them", view.Retentions)
	}

	older := idleSnapshots()
	older[0].Retention = retentionFacts(604800)
	view = Aggregate(Expectation{QueryGroups: 0, Known: true}, older, replicas(), now, freshness)
	if len(view.Retentions) != 2 {
		t.Fatalf("retention groups = %+v, want the facts beside a group for the replica that published none", view.Retentions)
	}
	var unreported *RetentionGroup
	for index := range view.Retentions {
		if view.Retentions[index].Retention == nil {
			unreported = &view.Retentions[index]
		}
	}
	if unreported == nil || len(unreported.Replicas) != 1 || unreported.Replicas[0] != "pod-b" {
		t.Fatalf("retention groups = %+v, want pod-b alone without facts", view.Retentions)
	}
}

// Two groups of one replica each that derive the same lengths from different
// inputs list in one order whichever replica the aggregate reads first: the
// replica whose name sorts first leads.
func TestRetentionGroupsListInOneOrderWhateverTheSnapshotOrder(t *testing.T) {
	longerReplay := retentionFacts(604800)
	longerReplay.MaxReplayAgeSeconds = 300
	for _, reversed := range []bool{false, true} {
		snapshots := idleSnapshots()
		snapshots[0].Retention, snapshots[1].Retention = retentionFacts(604800), longerReplay
		expected := replicas()
		if reversed {
			expected[0], expected[1] = expected[1], expected[0]
		}
		view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, expected, now, freshness)
		if len(view.Retentions) != 2 || view.Retentions[0].Replicas[0] != "pod-a" || view.Retentions[1].Replicas[0] != "pod-b" {
			t.Fatalf("reversed=%v: retention groups = %+v, want pod-a's group before pod-b's", reversed, view.Retentions)
		}
	}
}

// The health route carries the groups with every length and input by its
// runtime.get name, and an empty list rather than null with no replica.
func TestTheHealthRouteCarriesTheRetentionGroups(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].Retention, snapshots[1].Retention = retentionFacts(604800), retentionFacts(604800)
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	groups, ok := health["retentions"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("retentions = %v, want one group", health["retentions"])
	}
	group := groups[0].(map[string]any)
	facts, _ := group["retention"].(map[string]any)
	for name, want := range map[string]float64{"catalog_seconds": 86400, "object_limit_seconds": 604800,
		"snapshot_minimum_seconds": 86100, "redis_max_ttl_seconds": 604800, "max_replay_age_seconds": 120} {
		if facts[name] != want {
			t.Errorf("retention %s = %v, want %v", name, facts[name], want)
		}
	}
	if members, _ := group["replicas"].([]any); len(members) != 2 {
		t.Errorf("group replicas = %v, want both", group["replicas"])
	}

	handler = handlerWith(t, nil, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health = get(t, handler, "/api/health")
	if groups, ok := health["retentions"].([]any); !ok || len(groups) != 0 {
		t.Fatalf("retentions with no replicas = %v, want []", health["retentions"])
	}
}
