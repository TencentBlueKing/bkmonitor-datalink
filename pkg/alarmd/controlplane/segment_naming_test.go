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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Segment names the object the publication's manifest names.
//
// This is the invariant that was broken, and breaking it cost eleven hours.
// The Segment's digest used to be derived from the group handed to the cutter,
// and that group has been through the object store and back, while the
// manifest's digest was computed from the Catalog the leader built. Two
// derivations of one name. They agree exactly as long as assembly returns what
// was published -- and when assembly dropped a field, every Segment cut from
// an assembled group named an object the manifest did not, the cutover refused
// the mismatch on every later round, and the fleet stopped taking up published
// content while every other signal stayed healthy.
//
// The Segment copies the name now, so a field lost in assembly can no longer
// change one. This test holds the property from the outside: whatever the
// assembly does, the Segment and the manifest agree.
func TestASegmentNamesTheObjectTheManifestNames(t *testing.T) {
	for name, build := range map[string]func(*testing.T) controlplane.Catalog{
		"a catalog whose Plans detect no-data": noDataSourceCatalog,
		"a catalog whose Plans do not":         func(t *testing.T) controlplane.Catalog { return validCatalog(t, 80) },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newNoDataHopFixture(t, "segment-naming-"+shortPrefix(name), build(t))
			ctx := context.Background()

			manifest, err := fixture.repository.LoadCatalogManifest(ctx, publishedRevision(t, fixture))
			if err != nil {
				t.Fatal(err)
			}
			schedule, err := fixture.runtime.ReadFrozenSchedule(ctx, fixture.group, 60)
			if err != nil {
				t.Fatal(err)
			}
			var named string
			for _, entry := range manifest.QueryGroups {
				if entry.QueryGroup == fixture.group {
					named = string(entry.ObjectDigest)
				}
			}
			if named == "" {
				t.Fatalf("the manifest names no object for %s", fixture.group)
			}
			if got := string(schedule.Segment.ObjectDigest); got != named {
				t.Fatalf("the Segment names %s and the manifest names %s. A Segment that names an object "+
					"the publication does not is refused by every later cutover, and the fleet stops "+
					"taking up published content with nothing else reporting a problem", got, named)
			}
		})
	}
}

func publishedRevision(t *testing.T, fixture *noDataHopFixture) execution.SnapshotRevision {
	t.Helper()
	publication, err := fixture.repository.LoadLatestPublication(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return publication.SnapshotRevision
}

func shortPrefix(name string) string {
	if len(name) > 12 {
		name = name[:12]
	}
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == ' ' {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// A refused cutover writes nothing at all.
//
// Every one of the cutover's refusals happens inside the per-Query-Group loop,
// and both writes -- the timeline prune and the activation compare-and-set --
// come after it. That ordering is the whole reason a refusal is safe: it
// leaves the store exactly as it found it, so the next round sees the same
// thing and the failure stays one failure rather than becoming a half-applied
// publication. It is also ordering, which is what the next edit to a
// two-hundred-line function changes without noticing.
//
// Asserted on the stored bytes rather than on the decoded records, because
// "it returned before the write" is a claim about code order and the bytes are
// the only thing that can contradict it.
func TestARefusedCutoverWritesNothing(t *testing.T) {
	fixture := newNoDataHopFixture(t, "cutover-writes-nothing", validCatalog(t, 80))
	ctx := context.Background()

	timelineBefore, err := controlplane.ScheduleTimelineBytesForTest(ctx, fixture.repository, fixture.group)
	if err != nil {
		t.Fatal(err)
	}
	activationBefore, err := controlplane.ActivationBytesForTest(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}

	// The clock does not move, so the second publication cuts at the instant
	// the open Segment already starts at and the cutover refuses.
	second, _, err := fixture.repository.PublishCatalog(ctx, noDataSourceCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.reconciler.Ensure(ctx, second.Publication); err == nil {
		t.Skip("this fixture no longer produces a refusal; the assertion below would prove nothing")
	}

	timelineAfter, err := controlplane.ScheduleTimelineBytesForTest(ctx, fixture.repository, fixture.group)
	if err != nil {
		t.Fatal(err)
	}
	if string(timelineAfter) != string(timelineBefore) {
		t.Fatalf("a refused cutover changed the schedule timeline. Every refusal is inside the " +
			"per-Query-Group loop and both writes come after it; a refusal that writes leaves a " +
			"half-applied publication behind and the next round sees a different store than it did")
	}
	activationAfter, err := controlplane.ActivationBytesForTest(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	if string(activationAfter) != string(activationBefore) {
		t.Fatal("a refused cutover changed the activation")
	}
}

// Publishing the same content twice leaves the Segment where it is, and a
// worker reading it still gets the no-data section.
//
// The regression this closes end to end: a Segment named from the manifest, a
// second publication of identical content keeping it, and the section still
// present on the far side of the object store. Each of those was true in
// isolation while the chain was broken.
func TestRepublishingTheSameContentKeepsTheSegmentAndItsNoData(t *testing.T) {
	fixture := newNoDataHopFixture(t, "republish-keeps", noDataSourceCatalog(t))
	ctx := context.Background()

	// Publishing the same content again is the same publication: the revision
	// is derived from the content, so there is no second cutover to run and
	// nothing for it to keep. What the republish does establish is that the
	// second publication names the Segment the same way the first did, which
	// is the equality this regression is about.
	*fixture.now = fixture.now.Add(2 * time.Minute)
	again, _, err := fixture.repository.PublishCatalog(ctx, noDataSourceCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.reconciler.Ensure(ctx, again.Publication); err != nil {
		t.Fatal(err)
	}

	// The Segment names what both publications named.
	manifest, err := fixture.repository.LoadCatalogManifest(ctx, again.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	open, err := controlplane.OpenSegmentObjectDigestForTest(ctx, fixture.repository, fixture.group)
	if err != nil {
		t.Fatal(err)
	}
	var namedNow execution.ObjectDigest
	for _, entry := range manifest.QueryGroups {
		if entry.QueryGroup == fixture.group {
			namedNow = entry.ObjectDigest
		}
	}
	if open != namedNow {
		t.Fatalf("the Segment names %s and the publication names %s", open, namedNow)
	}

	// And what a worker reads back through it still detects no-data.
	schedule, err := fixture.runtime.ReadFrozenSchedule(ctx, fixture.group, 60)
	if err != nil {
		t.Fatal(err)
	}
	read, err := fixture.repository.LoadSegmentQueryGroup(ctx, schedule.Segment, 60,
		func(ctx context.Context) (controlplane.QueryGroup, error) {
			return controlplane.QueryGroup{}, errors.New("the content path was not taken")
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Plans) != 1 || read.Plans[0].Plan.NoData == nil {
		t.Fatalf("the Plan a worker reads through this Segment does not detect no-data: %+v", read.Plans)
	}
}
