// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group retired at one activation and brought back at a later one
// has a hole in its timeline: the retired Segment ends at the first
// boundary, the reactivated one starts at the second, and no Segment holds
// the time between. A Worker that began a Slot at the very second the
// retirement boundary fell on, and then found the Slot abandoned by the
// return, anchors its next continuous Slot on that time, which the closed
// Segment no longer holds. On the reference deployment twelve Query Groups
// did exactly that after a retire-and-return four minutes apart: every
// BeginSlot since failed with "schedule unavailable", nothing advanced the
// cursor, and the twelve were not evaluated again.
//
// The anchor is a fact about what completed; the hole is a fact about what
// never existed. Navigation from an anchor no Segment holds continues at the
// first Segment that starts after it, the way it continues from a retired
// Segment's end into its successor.
func TestNextSlotAfterAnAnchorInARetirementHoleContinuesAtTheNextSegment(t *testing.T) {
	harness := newPruneHarness(t, &pruneRetention)
	compiler, semantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(harness.repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// A runs from 60, is retired at 120 when only B is published, returns at
	// 180, is retired again at 300 and returns again at 360: Segments
	// [60,120), [180,300), [360, open); holes [120,180) and [300,360).
	a := harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 80, true, false), 60)
	harness.setProgress(a, 120)
	harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 80, false, true), 120)
	harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 81, true, true), 180)
	harness.setProgress(a, 300)
	harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 81, false, true), 300)
	harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 82, true, true), 360)
	shape := harness.timeline(t, a)
	if len(shape.segments) != 3 || shape.segments[1].Start != 180 || shape.segments[2].Start != 360 ||
		shape.lastReactivatedAfter == nil || *shape.lastReactivatedAfter != 300 {
		t.Fatalf("fixture did not produce two retirement holes: %+v after=%v", shape.segments, shape.lastReactivatedAfter)
	}

	for _, test := range []struct {
		anchor execution.EvaluationTime
		want   execution.EvaluationTime
		where  string
	}{
		{60, 180, "inside the first Segment, whose next Slot the retirement removed: continues into the returned Segment"},
		{119, 180, "at the end of the first Segment: continues into the returned Segment"},
		{120, 180, "on the first retirement boundary, inside the hole"},
		{150, 180, "inside the first hole"},
		{240, 360, "inside the returned Segment, whose next Slot the second retirement removed"},
		{300, 360, "on the second retirement boundary, inside the second hole"},
		{330, 360, "inside the second hole"},
		{360, 420, "inside the open Segment"},
	} {
		next, err := runtime.NextSlotAfter(harness.ctx, a, test.anchor)
		if err != nil {
			t.Fatalf("NextSlotAfter(anchor %d, %s) = %v, want %d", test.anchor, test.where, err, test.want)
		}
		if next != test.want {
			t.Fatalf("NextSlotAfter(anchor %d, %s) = %d, want %d", test.anchor, test.where, next, test.want)
		}
	}

	// The hole rule adds a continuation, not an answer where there is none:
	// past the end of a retired timeline with no return there is nothing to
	// continue at.
	harness.setProgress(a, 480)
	harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 82, false, true), 480)
	if _, err := runtime.NextSlotAfter(harness.ctx, a, 500); !errors.Is(err, controlplane.ErrScheduleUnavailable) {
		t.Fatalf("NextSlotAfter past a retired timeline = %v, want schedule unavailable", err)
	}
}

// The same condition, no Segment holding the anchor, has a second cause: the
// anchor's Segment was pruned. The continuation is the same, the first
// retained Segment's first Slot, and this is deliberate rather than a
// by-product: the Slots between a pruned anchor and the first retained
// Segment can never be read again, so there is nothing else to continue at.
// It is also bounded on purpose. An anchor is only consulted when the cursor
// has to be re-derived, and the Slot this returns can lie below a cursor
// that had already moved past it; BeginSlot then refuses the requested Slot
// as a conflict rather than beginning it. That shape is refused today too,
// only with a different word, and is left as it is here: this change adds a
// continuation where the timeline has one, it does not decide which of two
// disagreeing facts about the past to believe.
func TestNextSlotAfterAnAnchorBeforeThePrunedPrefixContinuesAtTheFirstRetainedSegment(t *testing.T) {
	harness := newPruneHarness(t, &pruneRetention)
	compiler, semantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(harness.repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Twelve one-minute Segments; by 780 s the first, [60,120), is past its
	// keep-until and Progress has moved past it, so the cutover prunes it.
	queryGroup := harness.publishAndActivate(t, 80, 60)
	harness.setProgress(queryGroup, 60)
	for index := 1; index < 12; index++ {
		harness.setProgress(queryGroup, execution.EvaluationTime(60+60*index))
		harness.publishAndActivate(t, 80+index, int64(60+60*index))
	}
	harness.setProgress(queryGroup, 720)
	harness.publishAndActivate(t, 200, 780)
	shape := harness.timeline(t, queryGroup)
	if len(shape.segments) == 0 || shape.segments[0].Start != 120 {
		t.Fatalf("fixture did not prune the first Segment: %+v", shape.segments)
	}

	for _, anchor := range []execution.EvaluationTime{30, 60, 119} {
		next, err := runtime.NextSlotAfter(harness.ctx, queryGroup, anchor)
		if err != nil {
			t.Fatalf("NextSlotAfter(anchor %d, before the pruned prefix) = %v, want the first retained Segment's first Slot 120", anchor, err)
		}
		if next != 120 {
			t.Fatalf("NextSlotAfter(anchor %d, before the pruned prefix) = %d, want 120", anchor, next)
		}
	}
}
