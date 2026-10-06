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
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A refresh round says how its Catalog was built. Every round used to compile
// every active strategy, whether or not its document had changed; a round
// that observes the same source now reports all of them as reused, and a
// round that observes one changed document reports exactly one compiled.
func TestSourceRefreshReportsWhatItCompiledAndWhatItReused(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001, 1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:compile-cache", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	first, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || first.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("first refresh = (%+v, %v)", first, err)
	}
	if first.CompiledStrategies != 2 || first.ReusedStrategies != 0 {
		t.Fatalf("first round compiled=%d reused=%d, want both documents compiled", first.CompiledStrategies, first.ReusedStrategies)
	}
	second, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || second.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("confirming refresh = (%+v, %v)", second, err)
	}
	if second.CompiledStrategies != 0 || second.ReusedStrategies != 2 {
		t.Fatalf("confirming round compiled=%d reused=%d, want nothing compiled for an unchanged source", second.CompiledStrategies, second.ReusedStrategies)
	}
	changed := bytes.Replace(documents[1], []byte(`"threshold":90`), []byte(`"threshold":95`), 1)
	if bytes.Equal(changed, documents[1]) {
		t.Fatal("the edit changed nothing; the fixture no longer carries the threshold this test moves")
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", string(changed), 0).Err(); err != nil {
		t.Fatal(err)
	}
	third, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || third.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("refresh after the edit = (%+v, %v)", third, err)
	}
	if third.CompiledStrategies != 1 || third.ReusedStrategies != 1 {
		t.Fatalf("round after the edit compiled=%d reused=%d, want exactly the edited document compiled", third.CompiledStrategies, third.ReusedStrategies)
	}
}
