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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A dry run lists what it would change and changes nothing.
//
// It is the default because this is a one-off operator action on live execution
// state: the first thing anyone wants is the list, and a command whose default
// writes is one that gets run before it is read.
func TestRepairingOpenSegmentsListsWithoutWritingByDefault(t *testing.T) {
	fixture := newNoDataHopFixture(t, "repair-dry", noDataSourceCatalog(t))
	ctx := context.Background()
	foreign := execution.ObjectDigest("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err := controlplane.SetOpenSegmentObjectDigestForTest(ctx, fixture.repository, fixture.group, foreign); err != nil {
		t.Fatal(err)
	}
	before, err := controlplane.ScheduleTimelineBytesForTest(ctx, fixture.repository, fixture.group)
	if err != nil {
		t.Fatal(err)
	}

	report, err := controlplane.RepairOpenSegments(ctx, fixture.repository, controlplane.RepairOpenSegmentsOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Repairs) != 1 || report.Repairs[0].QueryGroup != fixture.group {
		t.Fatalf("repairs = %+v, want the one Segment on a name the publication does not use", report.Repairs)
	}
	if report.Repairs[0].OpenDigest != foreign {
		t.Fatalf("the report names %s as the open digest, want %s", report.Repairs[0].OpenDigest, foreign)
	}
	if report.Applied != 0 {
		t.Fatalf("a dry run applied %d repairs", report.Applied)
	}
	after, err := controlplane.ScheduleTimelineBytesForTest(ctx, fixture.repository, fixture.group)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("a dry run changed the stored timeline")
	}
}

// Applying puts the Segment back on the published name, changes nothing else,
// and leaves the bytes it replaced where somebody can read them.
func TestApplyingARepairRestoresTheNameAndLeavesEvidence(t *testing.T) {
	fixture := newNoDataHopFixture(t, "repair-apply", noDataSourceCatalog(t))
	ctx := context.Background()
	published, err := controlplane.OpenSegmentObjectDigestForTest(ctx, fixture.repository, fixture.group)
	if err != nil {
		t.Fatal(err)
	}
	foreign := execution.ObjectDigest("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err := controlplane.SetOpenSegmentObjectDigestForTest(ctx, fixture.repository, fixture.group, foreign); err != nil {
		t.Fatal(err)
	}
	activationBefore, err := controlplane.ActivationBytesForTest(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()

	report, err := controlplane.RepairOpenSegments(ctx, fixture.repository,
		controlplane.RepairOpenSegmentsOptions{Apply: true, EvidenceDir: evidence})
	if err != nil {
		t.Fatal(err)
	}

	if report.Applied != 1 || report.Conflicts != 0 {
		t.Fatalf("report = %+v, want one applied and no conflicts", report)
	}
	restored, err := controlplane.OpenSegmentObjectDigestForTest(ctx, fixture.repository, fixture.group)
	if err != nil {
		t.Fatal(err)
	}
	if restored != published {
		t.Fatalf("the Segment now names %s, want the published %s", restored, published)
	}
	// The activation is untouched: moving it would change StateApplyEpoch and
	// put the whole fleet back into warmup, which is a far larger event than
	// the one being repaired.
	activationAfter, err := controlplane.ActivationBytesForTest(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	if string(activationAfter) != string(activationBefore) {
		t.Fatal("the repair changed the activation")
	}
	for _, stage := range []string{"before", "after"} {
		path := filepath.Join(evidence, string(fixture.group)+"."+stage+".json")
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("no %s evidence for %s: %v", stage, fixture.group, err)
		}
		if len(payload) == 0 {
			t.Fatalf("%s evidence for %s is empty", stage, fixture.group)
		}
	}
}

// A Segment already on the published name is not touched, and not reported.
func TestARepairSkipsSegmentsThatAreAlreadyRight(t *testing.T) {
	fixture := newNoDataHopFixture(t, "repair-clean", noDataSourceCatalog(t))
	ctx := context.Background()

	report, err := controlplane.RepairOpenSegments(ctx, fixture.repository,
		controlplane.RepairOpenSegmentsOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Repairs) != 0 {
		t.Fatalf("repairs = %+v, want none: the Segment already names what the publication names",
			report.Repairs)
	}
	if report.Scanned == 0 {
		t.Fatal("the run scanned no timelines at all, so finding nothing proves nothing")
	}
}

// Applying without somewhere to put the evidence is refused.
//
// A repair nobody can read back afterwards is a change nobody can check or
// undo, and this one rewrites live execution state by hand.
func TestApplyingARepairWithoutAnEvidenceDirectoryIsRefused(t *testing.T) {
	fixture := newNoDataHopFixture(t, "repair-noevidence", noDataSourceCatalog(t))

	_, err := controlplane.RepairOpenSegments(context.Background(), fixture.repository,
		controlplane.RepairOpenSegmentsOptions{Apply: true})

	if err == nil {
		t.Fatal("a repair was applied with nowhere to write what it replaced")
	}
}

// The repair refuses to overwrite bytes that moved since it read them.
//
// It is the cutover's fence, used by a command an operator runs by hand
// against live execution state while the leader is still running. Without it
// the repair is a last-writer-wins blind write, and the writer it would beat
// is the cutover it exists to unblock.
func TestTheRepairFenceRefusesBytesThatMoved(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:repair-fence", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := "alarmd:control:repair-fence:probe"
	original := []byte(`{"original":true}`)
	if err := client.Set(ctx, key, original, 0).Err(); err != nil {
		t.Fatal(err)
	}

	wrote, err := controlplane.RepairFenceForTest(ctx, repository, key,
		[]byte(`{"rewritten":true}`), []byte(`{"something":"else"}`))
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatal("the repair rewrote a key whose bytes are not the ones it read; the writer it would " +
			"beat is the cutover it exists to unblock")
	}
	after, err := client.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("the key changed anyway: %s", after)
	}

	// And with the bytes it did read, it writes -- otherwise the fence refuses
	// everything and the repair can never do anything.
	wrote, err = controlplane.RepairFenceForTest(ctx, repository, key, []byte(`{"rewritten":true}`), original)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("the repair refused bytes that had not moved")
	}
}
