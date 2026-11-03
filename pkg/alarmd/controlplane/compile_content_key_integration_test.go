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
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// contentKeyRecorder compiles through the real compiler and keeps the
// content key each Plan's compile was asked under, by strategy.
type contentKeyRecorder struct {
	next controlplane.RuntimePlanCompiler
	mu   sync.Mutex
	keys map[string]string
}

func (recorder *contentKeyRecorder) Compile(ctx context.Context, request strategy.CompileRequest) (strategy.CompileResult, error) {
	recorder.mu.Lock()
	recorder.keys[request.Plan.StrategyRef.StrategyID] = request.ContentKey
	recorder.mu.Unlock()
	return recorder.next.Compile(ctx, request)
}

// The freeze of a Slot whose group was read by content keys each Plan's
// compile by the group's object and that Plan's own output context; the
// freeze of one whose group came from the Snapshot keys none, so its compile
// derives the key from the Plan as it always did.
func TestAFrozenSlotKeysEachCompileByTheContentItsGroupWasReadFrom(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:compile-content-key"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	compiler, semantics := runtimePlanCompiler(t)
	at := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	catalog := objectCatalogTwoGroups(t, 80)
	snapshot, _, err := repository.PublishCatalog(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
		t.Fatal(err)
	}
	group := catalog.QueryGroups[0]

	freeze := func(on *controlplane.RedisCatalogRepository) (*contentKeyRecorder, execution.ScheduleSegmentFact) {
		t.Helper()
		recorder := &contentKeyRecorder{next: compiler, keys: map[string]string{}}
		runtime, err := controlplane.NewRedisCatalogRuntime(on, recorder, semantics, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		schedule, err := runtime.ReadFrozenSchedule(ctx, group.Identity, 60)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
			QueryGroup: group.Identity, ScheduleRevision: schedule.Segment.ScheduleRevision,
			ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: 60, DuePlans: schedule.DuePlanRefs(60),
		}); err != nil {
			t.Fatalf("FreezeSlotContract() error = %v", err)
		}
		if len(recorder.keys) == 0 {
			t.Fatal("setup: the freeze compiled no Plan")
		}
		return recorder, schedule.Segment.At(60)
	}

	byContent, segment := freeze(repository)
	if segment.ObjectDigest == "" {
		t.Fatal("setup: the activated Segment names no object")
	}
	checked := 0
	for _, plan := range group.Plans {
		key, compiled := byContent.keys[plan.Identity.StrategyID]
		if !compiled {
			continue
		}
		checked++
		context := segment.OutputContextRefFor(plan.Identity)
		if context == "" || !strings.HasPrefix(key, string(segment.ObjectDigest)+"\x00"+string(context)+"\x00") {
			t.Fatalf("Plan %s compiled under content key %q, want the Segment's object %s and its own context %s",
				plan.Identity.StrategyID, key, segment.ObjectDigest, context)
		}
	}

	if checked == 0 {
		t.Fatal("setup: no Plan of the group was compiled by the freeze")
	}

	// The persisted Segment rewritten, then the Slot frozen again by a
	// process that has not read it.
	timelineKey := prefix + ":schedule_timeline:" + string(group.Identity)
	stored, err := client.Get(ctx, timelineKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	refreeze := func(rewrite func(fields map[string]any)) (*contentKeyRecorder, execution.ScheduleSegmentFact) {
		t.Helper()
		var timeline map[string]any
		if err := json.Unmarshal(stored, &timeline); err != nil {
			t.Fatal(err)
		}
		segments, _ := timeline["segments"].([]any)
		for _, entry := range segments {
			schedule, _ := entry.(map[string]any)["schedule"].(map[string]any)
			fields, _ := schedule["Segment"].(map[string]any)
			if fields == nil {
				t.Fatalf("setup: persisted segment shape %v", entry)
			}
			rewrite(fields)
		}
		rewritten, err := json.Marshal(timeline)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, timelineKey, rewritten, 0).Err(); err != nil {
			t.Fatal(err)
		}
		cold, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return freeze(cold)
	}
	keyedNone := func(name string, recorder *contentKeyRecorder) {
		t.Helper()
		for strategyID, key := range recorder.keys {
			if key != "" {
				t.Fatalf("%s: Plan %s compiled under content key %q, want none: its group came from the Snapshot", name, strategyID, key)
			}
		}
	}

	// A Segment naming another group's object: the object is refused as
	// not this group's and the group is served from the Snapshot. The
	// Segment still names an object and contexts, so only the read's own
	// word keeps the compile from being keyed by content it did not use.
	other, err := controlplane.DeriveQueryGroupObjectDigest(catalog.QueryGroups[1])
	if err != nil {
		t.Fatal(err)
	}
	mismatched, mismatchedSegment := refreeze(func(fields map[string]any) { fields["ObjectDigest"] = string(other) })
	if mismatchedSegment.ObjectDigest != other {
		t.Fatalf("setup: the rewritten Segment names %s, want the other group's %s", mismatchedSegment.ObjectDigest, other)
	}
	keyedNone("a Segment naming another group's object", mismatched)

	// A Segment written before Segments named their content.
	legacy, legacySegment := refreeze(func(fields map[string]any) {
		delete(fields, "ObjectDigest")
		delete(fields, "OutputContextRefs")
	})
	if legacySegment.ObjectDigest != "" {
		t.Fatalf("setup: the rewritten Segment still names object %s", legacySegment.ObjectDigest)
	}
	keyedNone("a Segment naming no content", legacy)
}
