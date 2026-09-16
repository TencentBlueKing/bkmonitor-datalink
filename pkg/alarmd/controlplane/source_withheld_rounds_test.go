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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A new leader names every withheld object, even though the audit in Redis
// already records every one of them.
//
// This is the crossing the unit tests cannot reach: the comparand is chosen
// inside Refresh, and choosing the stored audit compiles, passes, and produces
// a leader that writes nothing. The audit is published state and outlives a
// leader; it records what was published, not what was written where an operator
// can read it. On the release that added these lines the two came apart exactly
// -- the audit was already there in full, published by leaders that had no such
// lines to write, so the first leader that could write them had nothing to
// report and said nothing at all.
//
// The failure is silent in the direction nobody checks: zero lines is also what
// a healthy steady state looks like, and what a feature that was never wired up
// looks like.
func TestANewLeaderNamesWithheldObjectsDespiteAPublishedAudit(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := runtimeCompileIsolationDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002,1003]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002", "1003"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:withheld-rounds", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// These limits withhold several of the objects, which is what gives the
	// test something to name.
	compiler, stateSemantics := runtimePlanCompilerWithLimits(t, 2, 1)
	newLeader := func() *controlplane.SourceReconciler {
		t.Helper()
		reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
		if err != nil {
			t.Fatal(err)
		}
		return reconciler
	}

	first := newLeader()
	opening, err := first.Refresh(ctx, source, planner)
	if err != nil {
		t.Fatal(err)
	}
	named := len(opening.Withheld.Lines)
	if named == 0 {
		t.Fatal("the opening round named no withheld object; every later assertion here would pass for " +
			"the wrong reason")
	}
	if opening.Withheld.Dropped != 0 {
		t.Fatalf("Dropped = %d on a round of %d, want nothing cut", opening.Withheld.Dropped, named)
	}

	// The same process, having said it once, says nothing more.
	for round := 0; round < 3; round++ {
		result, err := first.Refresh(ctx, source, planner)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Withheld.Lines) != 0 {
			t.Fatalf("round %d named %+v, want nothing: this process already said it and nothing changed",
				round+2, result.Withheld.Lines)
		}
	}
	// And it published, so the audit now records every disposition.
	if _, err := repository.LoadLatestAudit(ctx); err != nil {
		t.Fatalf("no audit was published, so this test cannot tell the two comparands apart: %v", err)
	}

	// A new leader, on that audit, over the same unchanged source.
	successor := newLeader()
	takeover, err := successor.Refresh(ctx, source, planner)
	if err != nil {
		t.Fatal(err)
	}
	if len(takeover.Withheld.Lines) != named {
		t.Fatalf("a new leader named %d withheld objects, want the %d the first one named. Nothing "+
			"changed in the source; what changed is who is reporting, and an operator arriving after a "+
			"failover needs the names rather than only the counts", len(takeover.Withheld.Lines), named)
	}
}
