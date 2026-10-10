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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A changed platform horizon empties the candidate cache, so every Plan is
// recompiled under the new setting.
//
// The cache is keyed by the strategy document, and a platform default is
// exactly the input that changes while every document stays put. Without the
// horizon in the round key the new default reaches only the strategies whose
// own document happens to change next: every other Plan keeps compiling with
// the old horizon, its object bytes do not move, its digest does not move, and
// the setting reads as applied while doing nothing. Configuring the horizon
// through the platform default is the ordinary way to configure it, so that
// failure is the feature being off by default and looking on.
func TestAChangedPlatformHorizonRecompilesEveryPlan(t *testing.T) {
	ctx := context.Background()
	strategies := cacheTestStrategies(t)
	planner := &recordingPlanner{facts: queryFacts(t)}
	cache := controlplane.NewCandidateCache()

	build := func(horizon int64) {
		t.Helper()
		if _, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{
			Strategies: strategies, Planner: planner, Cache: cache,
			NoDataPolicy: controlplane.NoDataPolicy{TrackingHorizonSeconds: horizon},
		}); err != nil {
			t.Fatal(err)
		}
	}

	build(600)
	if compiled, reused := cache.Stats(); compiled != 2 || reused != 0 {
		t.Fatalf("first round compiled=%d reused=%d, want everything compiled", compiled, reused)
	}
	// The control, and it has to come before the change: without it a cache
	// that never reused anything would pass the assertion below for the wrong
	// reason, and this case would say nothing about the horizon at all.
	build(600)
	if compiled, reused := cache.Stats(); compiled != 0 || reused != 2 {
		t.Fatalf("same horizon compiled=%d reused=%d, want everything reused", compiled, reused)
	}
	build(1200)
	if compiled, reused := cache.Stats(); compiled != 2 || reused != 0 {
		t.Fatalf("changed horizon compiled=%d reused=%d, want every Plan recompiled under the new default; "+
			"a reused Plan keeps the old horizon and nothing says so", compiled, reused)
	}
	// Back to no horizon at all is a change like any other. Zero is the value
	// the round key has to treat as a setting rather than as "unset", or
	// turning the horizon off would be the one change that does not take.
	build(0)
	if compiled, reused := cache.Stats(); compiled != 2 || reused != 0 {
		t.Fatalf("horizon removed compiled=%d reused=%d, want every Plan recompiled without it", compiled, reused)
	}
}

// The deployment's horizon reaches a Plan the reconciler published.
//
// The case above drives BuildCatalog with the policy handed to it, which is
// the one caller that cannot tell whether production has such a caller at all.
// It does not: the reconciler is what builds Catalogs in a running process, and
// until it passed the policy the horizon was a field every layer carried and
// nothing ever set. Everything downstream of it - the frozen config, the
// per-Plan override, the memory fields, the expiry sites - was complete and
// idle, because a horizon of zero means track indefinitely, so the feature read
// as off rather than as unwired.
//
// So this case starts at the reconciler deliberately. Asserting anywhere below
// it passes with the seam still cut.
func TestTheDeploymentHorizonReachesAPublishedPlan(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	document := withItemNoData(t, realThresholdDocuments(t)[0])
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:no-data-horizon", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)

	publish := func(policy func(*controlplane.SourceReconciler)) contract.EvaluationPlanV2 {
		t.Helper()
		reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
		if err != nil {
			t.Fatal(err)
		}
		policy(reconciler)
		if _, err := reconciler.Refresh(ctx, newRedisStrategySource(t, client), planner); err != nil {
			t.Fatal(err)
		}
		published, err := reconciler.Refresh(ctx, newRedisStrategySource(t, client), planner)
		if err != nil || published.Status != controlplane.SourceRefreshPublished {
			t.Fatalf("publish = (%#v, %v)", published, err)
		}
		snapshot, err := loadPublishedSnapshot(ctx, repository, published.Publication)
		if err != nil {
			t.Fatal(err)
		}
		plan := plansByStrategy(snapshot)["1001"].Plan
		if plan.NoData == nil {
			t.Fatal("the published Plan detects no no-data at all, so this case cannot say anything about " +
				"the horizon it carries")
		}
		return plan
	}

	configured := publish(func(reconciler *controlplane.SourceReconciler) {
		if err := reconciler.ConfigureNoDataPolicy(func() controlplane.NoDataPolicy {
			return controlplane.NoDataPolicy{TrackingHorizonSeconds: 600}
		}); err != nil {
			t.Fatal(err)
		}
	})
	if configured.NoData.TrackingHorizonSeconds != 600 {
		t.Fatalf("the published Plan carries horizon %d, want the deployment's 600. A Plan built without the "+
			"deployment's policy carries zero, which is indistinguishable from a deployment that chose to "+
			"track indefinitely", configured.NoData.TrackingHorizonSeconds)
	}

	// A deployment that configures nothing keeps the behaviour it had. This is
	// the other answer the branch has to give: without it the case above passes
	// on a reconciler that hands every Plan a horizon from somewhere else, and
	// the default this feature ships with - off - would go unasserted.
	silent := publish(func(*controlplane.SourceReconciler) {})
	if silent.NoData.TrackingHorizonSeconds != 0 {
		t.Fatalf("a deployment that configured no horizon published %d; absence must stay tracked "+
			"indefinitely, because a horizon stops no-data alerts and one nobody asked for hides an outage",
			silent.NoData.TrackingHorizonSeconds)
	}
}

// withItemNoData turns on no-data detection for the document's first item, the
// way the platform stores it: the section's presence is the enablement.
func withItemNoData(t *testing.T, document json.RawMessage) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	item := value["items"].([]any)[0].(map[string]any)
	if section, present := item["no_data_config"]; present && section != nil {
		t.Fatal("the fixture already configures no-data, so this helper would be deciding nothing")
	}
	item["no_data_config"] = map[string]any{"is_enabled": true, "continuous": 3}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
