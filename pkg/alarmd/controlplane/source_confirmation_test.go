// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// One consistent read decides: the active set read before the documents and
// again after them, equal, with every document read in between. The round
// that reads a change publishes it; there is no second read to wait for. The
// writer that states hold-last-good publishes a whole round in one
// transaction, so a read never sees half of one; the writer without a
// statement is the next test.
func TestAChangeIsPublishedByTheRoundThatReadsIt(t *testing.T) {
	harness := newChangeGateHarness(t)
	first := harness.refresh(controlplane.SourceRefreshPublished, controlplane.SourceReadFull, controlplane.SourceReadElected, 1)
	if plans := harness.publishedPlans(first.Publication); !reflect.DeepEqual(plans, []string{"1001", "1002"}) {
		t.Fatalf("first round published %v, want both strategies", plans)
	}
	// A round after a publication reads again: what it published may have
	// been read in the middle of a write.
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadPending, 1)

	harness.edit("1002", 1, `"threshold":90`, `"threshold":95`)
	harness.clock = harness.clock.Add(30 * time.Second)
	harness.signal(harness.clock)
	changed := harness.refresh(controlplane.SourceRefreshPublished, controlplane.SourceReadFull, controlplane.SourceReadChanged, 1)
	if changed.Observation == first.Observation || changed.Publication == first.Publication {
		t.Fatalf("the round that read the edit = %+v, want a new observation published", changed)
	}
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadPending, 1)
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
}

// The writer without a statement writes the strategy list before the
// documents. A round that reads between the two finds a strategy listed with
// no document: that strategy is withheld for the round (SOURCE_INCOMPLETE,
// and no last good Plan to keep since it is new), every other strategy keeps
// running, and nothing is removed. The writer finishes, moves its change
// signal, and the very next round publishes the new strategy: the half-read
// costs the new strategy one round and nobody else anything.
func TestAHalfWrittenListOnlyDelaysTheNewStrategyByARound(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.untilUnchanged("1003")

	// The list is written; the document of the strategy it adds is not yet.
	harness.setActiveSet(`[1001, 1002, 1003]`)
	half, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
	if err != nil {
		t.Fatalf("Refresh() of a half-written list error = %v", err)
	}
	if plans := harness.publishedPlans(half.Publication); !reflect.DeepEqual(plans, []string{"1001", "1002"}) {
		t.Fatalf("plans of the half-written list = %v, want 1001 and 1002 running and nothing removed", plans)
	}
	want := controlplane.ObjectDisposition{SourceID: "1003", Scope: "STRATEGY",
		Disposition: controlplane.DispositionSourceIncomplete, Reason: "SOURCE_OBJECT_INCOMPLETE"}
	if got := harness.strategyDisposition("1003"); got != want {
		t.Fatalf("audit of the half-written list = %+v, want %+v", got, want)
	}
	for _, id := range []string{"1001", "1002"} {
		if got := harness.strategyDisposition(id); got != (controlplane.ObjectDisposition{}) {
			t.Fatalf("audit of %s on the half-written list = %+v, want nothing withheld or removed", id, got)
		}
	}

	// The writer finishes the round: the document, then its change signal.
	var document map[string]any
	if err := json.Unmarshal(harness.documents[0], &document); err != nil {
		t.Fatal(err)
	}
	document["id"] = 1003
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.client.Set(harness.ctx, "bkmonitor.cache.strategy_1003", string(payload), 0).Err(); err != nil {
		t.Fatal(err)
	}
	harness.clock = harness.clock.Add(30 * time.Second)
	harness.signal(harness.clock)
	next, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
	if err != nil || next.Status != controlplane.SourceRefreshPublished || next.ReadMode != controlplane.SourceReadFull {
		t.Fatalf("Refresh() after the writer finished = (%+v, %v), want the next round to read and publish", next, err)
	}
	if plans := harness.publishedPlans(next.Publication); !reflect.DeepEqual(plans, []string{"1001", "1002", "1003"}) {
		t.Fatalf("plans after the writer finished = %v, want 1003 published on the next round", plans)
	}
}
