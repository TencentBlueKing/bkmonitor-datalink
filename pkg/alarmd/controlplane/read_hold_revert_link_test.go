package controlplane_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A time_delay edit reverted brings the original group back. Its links must
// still name another group: a link to itself is refused on every read, and
// the group could never prepare a Slot.
func TestARevertedDelayEditLeavesNoLinkToItself(t *testing.T) {
	for _, arm := range []struct {
		name   string
		revert int64
	}{{"edited group never ran", 122}, {"edited group ran a Slot", 200}} {
		t.Run(arm.name, func(t *testing.T) {
			ctx := context.Background()
			client := newControlplaneRedis(t)
			repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:read-hold-revert", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
			compiler, semantics := runtimePlanCompiler(t)
			now := time.Unix(60, 0)
			reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			publish := func(catalog controlplane.Catalog, at int64) {
				now = time.Unix(at, 0)
				snapshot, _, err := repository.PublishCatalog(ctx, catalog)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
					t.Fatalf("activation at %d: %v", at, err)
				}
			}
			drained := func(qg execution.QueryGroupIdentity, next, last int64) {
				progress.byGroup[qg] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
					Identity: execution.ProgressIdentity{QueryGroup: qg}, NextSlot: execution.EvaluationTime(next),
					LastFullSlot: execution.EvaluationTime(last), LastCompletionKind: execution.CompletionFull}}
			}
			first := readHoldCatalog(t, 0, 80)
			original := first.QueryGroups[0].Identity
			drained(original, 120, 60)
			publish(first, 60)
			edited := readHoldCatalog(t, 120, 80)
			drained(edited.QueryGroups[0].Identity, 180, 0)
			publish(edited, 121)
			drained(original, 180, 120)
			if arm.revert > 180 {
				drained(edited.QueryGroups[0].Identity, 240, 180)
			}
			publish(readHoldCatalog(t, 0, 80), arm.revert)
			schedule, err := runtime.ReadFrozenSchedule(ctx, original, execution.EvaluationTime(arm.revert))
			if err != nil {
				t.Fatalf("the original group did not come back at %d: %v", arm.revert, err)
			}
			refs, _, err := repository.ReadHoldPredecessors(ctx, schedule)
			t.Logf("links of the returned group: %+v %v", refs, err)
			if err != nil {
				t.Fatalf("the returned group's links cannot be read, so it can never prepare a Slot: %v", err)
			}
			for _, ref := range refs {
				if ref.QueryGroup == original {
					t.Fatalf("the returned group links to itself: %+v", ref)
				}
			}
		})
	}
}
