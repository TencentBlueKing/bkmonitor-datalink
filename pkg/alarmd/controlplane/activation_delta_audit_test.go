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

// A delta names what its activation wrote; a Worker needs what changed. The
// two agree only while that script is the only writer of timelines, which is
// a claim about the whole system. So every sixteenth header change crossed
// by delta is audited against the timelines themselves, in two directions
// that are never summed: a kept timeline whose record moved is one the delta
// missed (stale content served), a dropped one whose record did not move is
// one it over-named (a wasted read). Agreement is counted too, so a zero in
// either direction can be told from no audit having run.
func TestSampledDeltaAuditTellsMissedFromOverNamed(t *testing.T) {
	const auditEvery = 16
	for _, test := range []struct {
		name       string
		tamper     func(named []execution.QueryGroupIdentity, a, b execution.QueryGroupIdentity) []execution.QueryGroupIdentity
		wantAudit  controlplane.ControlDeltaAuditStats
		wantBodies int
	}{
		// Bodies read on the audited crossing: the audit reads both cached
		// timelines, then B is served or re-read and A is re-read or served
		// depending on what the delta said.
		{name: "an exact delta agrees", tamper: nil,
			wantAudit: controlplane.ControlDeltaAuditStats{Samples: 1, Agreed: 1}, wantBodies: 3},
		{name: "a delta that names nothing missed the change",
			tamper: func([]execution.QueryGroupIdentity, execution.QueryGroupIdentity, execution.QueryGroupIdentity) []execution.QueryGroupIdentity {
				return []execution.QueryGroupIdentity{}
			},
			wantAudit: controlplane.ControlDeltaAuditStats{Samples: 1, Missed: 1}, wantBodies: 2},
		{name: "a delta that names the unchanged one over-named it",
			tamper: func(_ []execution.QueryGroupIdentity, a, b execution.QueryGroupIdentity) []execution.QueryGroupIdentity {
				return []execution.QueryGroupIdentity{a, b}
			},
			wantAudit: controlplane.ControlDeltaAuditStats{Samples: 1, OverNamed: 1}, wantBodies: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			client := newControlplaneRedis(t)
			prefix := "alarmd:control:delta-audit"
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
			worker, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := controlplane.NewRedisCatalogRuntime(worker, compiler, semantics, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			initial := catalogWithAllSchedules(t, twoGroupCatalog(t, 80, true, true), 60, 0)
			a := catalogWithAllSchedules(t, twoGroupCatalog(t, 80, true, false), 60, 0).QueryGroups[0].Identity
			var b execution.QueryGroupIdentity
			for _, group := range initial.QueryGroups {
				if group.Identity != a {
					b = group.Identity
				}
			}
			snapshot, _, err := leader.PublishCatalog(ctx, initial)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
				t.Fatal(err)
			}
			read := func(group execution.QueryGroupIdentity) {
				t.Helper()
				if _, err := runtime.ReadFrozenSchedule(ctx, group, execution.EvaluationTime(at.Unix())); err != nil {
					t.Fatal(err)
				}
			}
			read(a)
			read(b)
			hook := newControlReadCountingHook()
			client.AddHook(hook)

			// Fifteen edits of A cross fifteen headers by delta, none audited.
			// The sixteenth is audited, after the test rewrites its delta.
			for crossing := 1; crossing <= auditEvery; crossing++ {
				at = at.Add(time.Minute)
				edited := catalogWithAllSchedules(t, twoGroupCatalog(t, 80+crossing, true, true), 60, 0)
				published, _, err := leader.PublishCatalog(ctx, edited)
				if err != nil {
					t.Fatal(err)
				}
				state, err := reconciler.Ensure(ctx, published.Publication)
				if err != nil {
					t.Fatal(err)
				}
				if crossing == auditEvery && test.tamper != nil {
					key := prefix + ":activation_delta:" + strconv.FormatUint(state.RecordRevision, 10)
					payload, err := json.Marshal(map[string]any{
						"schema_version": "alarmd-control-activation-delta-v1", "record_revision": state.RecordRevision,
						"query_groups": test.tamper([]execution.QueryGroupIdentity{a}, a, b),
					})
					if err != nil {
						t.Fatal(err)
					}
					if err := client.Set(ctx, key, payload, time.Hour).Err(); err != nil {
						t.Fatal(err)
					}
				}
				hook.reset()
				read(b)
				if crossing < auditEvery {
					if got := hook.bodyReads("timeline"); got != 0 {
						t.Fatalf("crossing %d read %d timeline bodies for the unchanged Query Group, want 0", crossing, got)
					}
					if stats := worker.ControlReadCacheStats(); stats.DeltaAudit.Samples != 0 {
						t.Fatalf("crossing %d was audited: %+v", crossing, stats.DeltaAudit)
					}
				}
				// A is read back every crossing so that both timelines are
				// cached when the audited crossing comes.
				read(a)
			}
			if got := hook.bodyReads("timeline"); got != test.wantBodies {
				t.Fatalf("the audited crossing read %d timeline bodies, want %d", got, test.wantBodies)
			}
			stats := worker.ControlReadCacheStats()
			if stats.DeltaAudit != test.wantAudit {
				t.Fatalf("delta audit = %+v, want %+v", stats.DeltaAudit, test.wantAudit)
			}
			if stats.Delta.Hits != auditEvery {
				t.Fatalf("headers crossed by delta = %d, want %d", stats.Delta.Hits, auditEvery)
			}
		})
	}
}
