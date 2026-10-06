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
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type activationDeltaRecord struct {
	SchemaVersion  string                         `json:"schema_version"`
	RecordRevision uint64                         `json:"record_revision"`
	Full           bool                           `json:"full,omitempty"`
	QueryGroups    []execution.QueryGroupIdentity `json:"query_groups,omitempty"`
}

// Every activation used to cost every Worker one read of every owned
// timeline: the control header changed, so the whole control read cache
// went. The activation now writes, in the same script as the timelines it
// changes, the list of Query Groups it changed. A Worker that sees the new
// header drops those and keeps the rest; a delta it cannot read, or one that
// says full, sends it back to dropping everything.
func TestWorkerKeepsTheTimelinesAnActivationDidNotChange(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:activation-delta"
	leader, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	at := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconciler(leader, compiler, semantics, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	both := catalogWithAllSchedules(t, twoGroupCatalog(t, 80, true, true), 60, 0)
	// onlyA keeps A at the edited threshold, so retiring B is the only change.
	onlyA := catalogWithAllSchedules(t, twoGroupCatalog(t, 82, true, false), 60, 0)
	edited := catalogWithAllSchedules(t, twoGroupCatalog(t, 82, true, true), 60, 0)
	a := onlyA.QueryGroups[0].Identity
	var b execution.QueryGroupIdentity
	for _, group := range both.QueryGroups {
		if group.Identity != a {
			b = group.Identity
		}
	}
	publishAndActivate := func(catalog controlplane.Catalog) controlplane.ActivationState {
		t.Helper()
		snapshot, _, err := leader.PublishCatalog(ctx, catalog)
		if err != nil {
			t.Fatal(err)
		}
		state, err := reconciler.Ensure(ctx, snapshot.Publication)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	deltaOf := func(revision uint64) activationDeltaRecord {
		t.Helper()
		payload, err := client.Get(ctx, prefix+":activation_delta:"+strconv.FormatUint(revision, 10)).Bytes()
		if err != nil {
			t.Fatalf("activation delta for revision %d: %v", revision, err)
		}
		var delta activationDeltaRecord
		if err := json.Unmarshal(payload, &delta); err != nil {
			t.Fatal(err)
		}
		return delta
	}

	// A Worker with both timelines cached.
	worker, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(worker, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	initial := publishAndActivate(both)
	if _, err := worker.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
	for _, group := range []execution.QueryGroupIdentity{a, b} {
		if _, err := runtime.ReadFrozenSchedule(ctx, group, 120); err != nil {
			t.Fatal(err)
		}
	}
	hook := newControlReadCountingHook()
	client.AddHook(hook)

	// A is edited, B is not: the delta names A alone, and the Worker reads A
	// again while B comes from its cache across the header change.
	at = time.Unix(180, 0)
	editedState := publishAndActivate(edited)
	if editedState.RecordRevision != initial.RecordRevision+1 {
		t.Fatalf("edited activation revision = %d, want %d", editedState.RecordRevision, initial.RecordRevision+1)
	}
	if delta := deltaOf(editedState.RecordRevision); delta.Full || len(delta.QueryGroups) != 1 || delta.QueryGroups[0] != a ||
		delta.RecordRevision != editedState.RecordRevision {
		t.Fatalf("delta of the edit = %+v, want exactly a", delta)
	}
	hook.reset()
	if _, err := runtime.ReadFrozenSchedule(ctx, b, 180); err != nil {
		t.Fatal(err)
	}
	if got := hook.bodyReads("timeline"); got != 0 {
		t.Fatalf("reading the unchanged timeline after the header change read %d bodies, want 0", got)
	}
	if _, err := runtime.ReadFrozenSchedule(ctx, a, 180); err != nil {
		t.Fatal(err)
	}
	if got := hook.bodyReads("timeline"); got != 1 {
		t.Fatalf("reading the edited timeline after the header change read %d bodies, want 1", got)
	}
	if stats := worker.ControlReadCacheStats(); stats.Delta != (controlplane.ControlReadCacheObjectStats{Hits: 1}) {
		t.Fatalf("delta stats after one header crossed by delta = %+v, want one hit", stats.Delta)
	}

	// B is retired: the delta names B, whose timeline the activation closed.
	at = time.Unix(240, 0)
	retiredState := publishAndActivate(onlyA)
	if delta := deltaOf(retiredState.RecordRevision); delta.Full || len(delta.QueryGroups) != 1 || delta.QueryGroups[0] != b {
		t.Fatalf("delta of the retirement = %+v, want exactly b", delta)
	}
	hook.reset()
	if _, err := runtime.ReadFrozenSchedule(ctx, a, 240); err != nil {
		t.Fatal(err)
	}
	if got := hook.bodyReads("timeline"); got != 0 {
		t.Fatalf("a's timeline was read again after an activation that did not touch it: %d bodies", got)
	}

	// The delta is gone (a leader that did not write one): the Worker drops
	// everything, as it did before deltas existed, and says so.
	at = time.Unix(300, 0)
	backState := publishAndActivate(catalogWithAllSchedules(t, twoGroupCatalog(t, 84, true, false), 60, 0))
	if err := client.Del(ctx, prefix+":activation_delta:"+strconv.FormatUint(backState.RecordRevision, 10)).Err(); err != nil {
		t.Fatal(err)
	}
	hook.reset()
	if _, err := runtime.ReadFrozenSchedule(ctx, a, 300); err != nil {
		t.Fatal(err)
	}
	if got := hook.bodyReads("timeline"); got != 1 {
		t.Fatalf("without a delta the Worker kept a timeline across the header change: %d bodies read, want 1", got)
	}
	if stats := worker.ControlReadCacheStats(); stats.Delta != (controlplane.ControlReadCacheObjectStats{Hits: 2, Misses: 1}) {
		t.Fatalf("delta stats = %+v, want two headers crossed by delta and one by dropping everything", stats.Delta)
	}
}
