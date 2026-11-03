// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Same-boundary cutover. Revision 1 is activated at
// second 60, both Query Groups opening there; the next publication leaves
// one of them and edits the other, activated at a second at or before 60.

// currentGroups is the activation's current active Query Group set.
func (fixture *cutoverFixture) currentGroups(t *testing.T, state controlplane.ActivationState) map[execution.QueryGroupIdentity]bool {
	t.Helper()
	groups, err := fixture.repository.LoadActiveQueryGroupSet(fixture.ctx, state.ActiveQGSetRef)
	if err != nil {
		t.Fatal(err)
	}
	current := make(map[execution.QueryGroupIdentity]bool, len(groups))
	for _, group := range groups {
		current[group] = true
	}
	return current
}

// At or within MaxClockSkew behind the open Segments' start, the cutover
// moves to the first second past them: the leaving Query Group is retired
// there, drains from there and is not current; the edited one is cut there
// to the new content, not held back.
func TestASameBoundaryCutoverMovesPastTheOpenSegments(t *testing.T) {
	for _, tc := range []struct {
		name, slug string
		at         int64
	}{
		{"same second", "same-second", 60},
		{"clock stepped back", "stepped-back", 57},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newCutoverFixture(t, "alarmd:control:same-boundary-"+tc.slug)
			first := cutoverCatalog(t, 80, nil)
			second := cutoverCatalog(t, 90, nil)
			edited, untouched := splitEdited(t, first, second)
			fixture.publish(t, first, 60)
			var results []string
			fixture.repository.ConfigureObserver(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if facts := observation.ScheduleCutover; facts != nil {
					results = append(results, facts.Result+":"+facts.Reason)
				}
			}))
			leaving := catalogWithout(t, second, untouched.Identity)
			snapshot := fixture.publish(t, leaving, tc.at)
			// The refused cut is named, and the one past it lands.
			if len(results) != 2 || results[0] != "failure:"+controlplane.CutoverReasonBoundaryBehindOpenSegment || results[1] != "success:" {
				t.Fatalf("cutovers = %v, want one refused as %s, then one that lands", results, controlplane.CutoverReasonBoundaryBehindOpenSegment)
			}

			state := fixture.activation(t)
			if len(state.Draining) != 1 || state.Draining[0] != (controlplane.DrainingQueryGroup{QueryGroup: untouched.Identity, RetiredBoundary: 61}) {
				t.Fatalf("draining = %+v, want %s retired at 61", state.Draining, untouched.Identity)
			}
			retiredAt, retired, err := fixture.runtime.ReadScheduleRetirement(fixture.ctx, untouched.Identity)
			if err != nil || !retired || retiredAt != 61 {
				t.Fatalf("the leaving Query Group's timeline is retired (%d, %v, %v), want at 61", retiredAt, retired, err)
			}
			// Its Plans are current up to the retirement and not after it.
			if segment := fixture.openSegment(t, untouched.Identity, 60); segment.End == nil || *segment.End != 61 {
				t.Fatalf("the leaving Query Group's Segment = %+v, want closed at 61", segment)
			}
			// A maintenance read before the boundary answers the old Plans
			// with the Segment's end, past which they are not judged.
			if read, err := fixture.runtime.CurrentPlans(fixture.ctx, untouched.Identity, 60); err != nil || len(read.Plans) == 0 || read.Until != 61 {
				t.Fatalf("the leaving Query Group's Plans at 60 = (%d Plans until %d, %v), want its Plans until 61", len(read.Plans), read.Until, err)
			}
			if _, err := fixture.runtime.ReadFrozenSchedule(fixture.ctx, untouched.Identity, 61); !errors.Is(err, controlplane.ErrScheduleUnavailable) {
				t.Fatalf("the leaving Query Group's schedule at 61 = %v, want none", err)
			}
			if current := fixture.currentGroups(t, state); current[untouched.Identity] || !current[edited.Identity] || len(current) != 1 {
				t.Fatalf("current = %v, want %s alone", current, edited.Identity)
			}
			if cut := fixture.openSegment(t, edited.Identity, 61); cut.Start != 61 || cut.Publication.SnapshotRevision != snapshot.Publication.SnapshotRevision {
				t.Fatalf("the edited Query Group's open Segment = %+v, want cut at 61 to the new publication", cut)
			}
			if blocked := fixture.blocked(t); len(blocked) != 0 {
				t.Fatalf("held back: %+v", blocked)
			}
		})
	}
}

// Further behind than MaxClockSkew the cutover does not move: the leaving
// Query Group's timeline is left as it is (retired_unwritten), as before -
// but it is draining, and a draining Query Group is never current.
func TestALeavingQueryGroupTheCutoverCannotRetireIsNotCurrent(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:same-boundary-far")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	before := fixture.timelineBytes(t, untouched.Identity)
	if _, err := fixture.publishNoEnsure(t, catalogWithout(t, second, untouched.Identity), 50); err != nil {
		t.Fatal(err)
	}
	state := fixture.activation(t)
	if len(state.Draining) != 1 || state.Draining[0].QueryGroup != untouched.Identity {
		t.Fatalf("draining = %+v, want %s", state.Draining, untouched.Identity)
	}
	if after := fixture.timelineBytes(t, untouched.Identity); string(after) != string(before) {
		t.Fatal("the leaving Query Group's timeline was written")
	}
	if current := fixture.currentGroups(t, state); current[untouched.Identity] {
		t.Fatalf("current = %v, want the draining %s out of it", current, untouched.Identity)
	}
}
