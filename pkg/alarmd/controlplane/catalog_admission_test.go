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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// What the deployment's admission returns is what gets published.
//
// The hook used to be able only to refuse the round, and the reconciler only
// had to look at its error. Now it also decides which Plans the deployment can
// serve, and the Catalog it hands back is the one that must be published --
// a reconciler that keeps the Catalog it built instead would publish exactly
// the Plans the deployment just said it cannot run, silently, while the
// dispositions said otherwise.
func TestThePublishedCatalogIsTheOneAdmissionReturned(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for id, document := range map[string]json.RawMessage{"1001": documents[0], "1002": documents[1]} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(document), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:admission", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)

	// An admission that withholds one strategy and keeps the other, which is
	// the shape a deployment uses for a Plan it cannot serve.
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics,
		func(catalog controlplane.Catalog) (controlplane.Catalog, error) {
			// Withholds the Plan, not the Query Group: two strategies sharing
			// one query share one group, which is the ordinary shape and the
			// one a group-level filter gets wrong.
			groups := make([]controlplane.QueryGroup, 0, len(catalog.QueryGroups))
			for _, group := range catalog.QueryGroups {
				kept := make([]controlplane.FrozenPlan, 0, len(group.Plans))
				for _, plan := range group.Plans {
					if plan.Identity.StrategyID == "1001" {
						catalog.Dispositions = append(catalog.Dispositions, controlplane.ObjectDisposition{
							SourceID: "1001", Scope: "PLAN", Disposition: controlplane.DispositionUnsupported,
							Reason: "SNAPSHOT_RETENTION_INSUFFICIENT",
						})
						continue
					}
					kept = append(kept, plan)
				}
				if len(kept) == 0 {
					continue
				}
				group.Plans = kept
				groups = append(groups, group)
			}
			catalog.QueryGroups = groups
			return catalog, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil ||
		result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("first refresh = (%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("publish = (%#v, %v)", published, err)
	}

	snapshot, err := loadPublishedSnapshot(ctx, repository, published.Publication)
	if err != nil {
		t.Fatal(err)
	}
	plans := plansByStrategy(snapshot)
	if _, withheld := plans["1001"]; withheld {
		t.Fatalf("the withheld strategy was published anyway: %#v. The admission's Catalog is the one "+
			"that must be published; keeping the built one publishes exactly the Plans the deployment "+
			"said it cannot run", snapshot.QueryGroups)
	}
	if _, kept := plans["1002"]; !kept {
		t.Fatalf("the admitted strategy is missing: %#v. One Plan a deployment cannot serve must not "+
			"take the others with it", snapshot.QueryGroups)
	}
	assertAuditDispositionExact(t, repository, "1001", "PLAN", 0,
		controlplane.DispositionUnsupported, "SNAPSHOT_RETENTION_INSUFFICIENT")
}
