// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A later build writes the activation body as a head - no Plan records, the
// records being on the open Segments - and may leave a publication cutover
// committed in pieces unfinished (N15). This build is what a rollback lands
// on, so it must read both. These pin that it does.

// headCatalog builds a catalog from any of three strategies in three
// businesses, so three Query Groups: A (business 2, its threshold given),
// B (business 3) and C (business 4).
func headCatalog(t *testing.T, thresholdA int, sources string) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	withBusiness := func(document json.RawMessage, business string, id int) []byte {
		var decoded map[string]any
		if err := json.Unmarshal(withWireIdentity(t, document, "tenant-a", "bkcc__"+business), &decoded); err != nil {
			t.Fatal(err)
		}
		biz, _ := strconv.Atoi(business)
		decoded["bk_biz_id"] = float64(biz)
		decoded["id"] = float64(id)
		encoded, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	all := map[byte]controlplane.SourceStrategy{
		'A': {SourceID: "1001", Document: []byte(strings.Replace(string(documents[0]), `"threshold":80`, `"threshold":`+strconv.Itoa(thresholdA), 1)),
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		'B': {SourceID: "1002", Document: withBusiness(documents[1], "3", 1002),
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
		'C': {SourceID: "1003", Document: withBusiness(documents[1], "4", 1003),
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "4", SpaceScope: "bkcc__4"}},
	}
	strategies := make([]controlplane.SourceStrategy, 0, len(sources))
	for index := 0; index < len(sources); index++ {
		strategies = append(strategies, all[sources[index]])
	}
	planner := &businessPlanner{facts: map[string]execution.QueryPlanFacts{
		"2": queryFactsFor(t, "2", "bkcc__2"), "3": queryFactsFor(t, "3", "bkcc__3"), "4": queryFactsFor(t, "4", "bkcc__4")}}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: strategies, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != len(sources) {
		t.Fatalf("expected %d Query Groups, got %d with dispositions %+v", len(sources), len(catalog.QueryGroups), catalog.Dispositions)
	}
	return catalog
}

// groupOf is the Query Group of a catalog that carries the given business.
func groupOf(t *testing.T, catalog controlplane.Catalog, business string) controlplane.QueryGroup {
	t.Helper()
	for _, group := range catalog.QueryGroups {
		if group.Plans[0].Identity.BusinessID == business {
			return group
		}
	}
	t.Fatalf("no Query Group for business %s", business)
	return controlplane.QueryGroup{}
}

// asHead is the body a later build writes for the same activation: no
// records, schema v3.
func asHead(state controlplane.ActivationState) controlplane.ActivationState {
	state.SchemaVersion = controlplane.ActivationHeadSchemaVersionForTest
	state.Plans = nil
	return state
}

// A head body is read back whole: readers of the head get no records, the
// Control Leader gets the records its open Segments carry, a worker is
// answered from the timeline, and the next publication cuts over from it
// and writes the body this build writes.
func TestAHeadBodyIsReadBackFromTheOpenSegments(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:activation-head")
	first := headCatalog(t, 80, "AB")
	fixture.publish(t, first, 60)
	before := fixture.activation(t)
	if err := controlplane.WriteActivationForTest(fixture.ctx, fixture.repository, asHead(before)); err != nil {
		t.Fatal(err)
	}

	head, err := fixture.repository.LoadActivationHead(fixture.ctx)
	if err != nil || head.Plans != nil || head.Current != before.Current || head.RecordRevision != before.RecordRevision ||
		head.ActiveQGSetRef != before.ActiveQGSetRef {
		t.Fatalf("head = (%+v, %v), want the same activation without records", head, err)
	}
	full := fixture.activation(t)
	if full.SchemaVersion != before.SchemaVersion || !reflect.DeepEqual(sortedRecords(full.Plans), sortedRecords(before.Plans)) {
		t.Fatalf("records read back from the open Segments:\n got=%+v\nwant=%+v", full.Plans, before.Plans)
	}

	// A worker without a revision hint is answered from the timeline.
	groupA := groupOf(t, first, "2")
	segment := fixture.openSegment(t, groupA.Identity, 60)
	contract := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: groupA.Identity, EvaluationTime: 60},
		SnapshotRevision: segment.Publication.SnapshotRevision, QueryRevision: segment.QueryRevision,
		ScheduleRevision: segment.ScheduleRevision, ScheduleSegmentStart: segment.Start, DuePlanSetDigest: "due-plans-v1",
	}
	want := recordsOf(before, groupA)
	answer, err := fixture.repository.LoadActivations(fixture.ctx, execution.PlanActivationRequest{
		Contract: contract, Plans: []execution.PlanKey{want[0].Fact.Key()}})
	if err != nil || len(answer.Facts) != 1 || !answer.Facts[0].Equal(want[0].Fact) {
		t.Fatalf("worker answer = (%+v, %v), want %+v", answer.Facts, err, want[0].Fact)
	}

	// The next publication cuts over from the head and writes a head
	// again; the records it wrote are the ones the leader reads back.
	second := headCatalog(t, 90, "AB")
	fixture.publish(t, second, 120)
	after := fixture.activation(t)
	raw, err := controlplane.ActivationBytesForTest(fixture.ctx, fixture.repository)
	if err != nil || !strings.Contains(string(raw), `"alarmd-control-activation-v3"`) || strings.Contains(string(raw), `"fact"`) ||
		len(after.Plans) != len(before.Plans) {
		t.Fatalf("after the next publication body=%s plans=%d (%v)", raw, len(after.Plans), err)
	}
	if cut := fixture.openSegment(t, groupA.Identity, 120); cut.Start != 120 {
		t.Fatalf("the edited Query Group was not cut: %+v", cut)
	}

	// A head that carries records is not a head.
	corrupt := asHead(after)
	corrupt.Plans = after.Plans
	corrupt.RecordRevision++
	if err := controlplane.WriteActivationForTest(fixture.ctx, fixture.repository, corrupt); err != nil {
		t.Fatal(err)
	}
	var corruptErr *controlplane.PersistedActivationCorruptError
	if _, err := fixture.repository.LoadActivationHead(fixture.ctx); !errors.As(err, &corruptErr) {
		t.Fatalf("a head with records read as %v, want corrupt", err)
	}
}
