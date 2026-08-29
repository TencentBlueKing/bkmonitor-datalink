// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestCompilerCachesPositiveAndNegativeResults(t *testing.T) {
	compiler := newTestCompiler(t)
	request := validRequest(validPlan())
	for range 2 {
		if _, err := compiler.Compile(context.Background(), request); err != nil {
			t.Fatalf("Compile() error = %v", err)
		}
	}
	stats := compiler.CacheStats()
	if stats.Misses != 1 || stats.Hits != 1 || stats.Entries != 1 || stats.NegativeHits != 0 {
		t.Fatalf("positive CacheStats() = %+v", stats)
	}

	bad := validPlan()
	bad.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Type = "Unknown"
	request = validRequest(bad)
	for range 2 {
		if _, err := compiler.Compile(context.Background(), request); err != nil {
			t.Fatalf("Compile() error = %v", err)
		}
	}
	stats = compiler.CacheStats()
	if stats.Misses != 2 || stats.Hits != 2 || stats.NegativeHits != 1 || stats.Entries != 2 {
		t.Fatalf("negative CacheStats() = %+v", stats)
	}
}

func TestCompileCacheEnforcesLRUAndNegativeTTL(t *testing.T) {
	cache := newCompileCache(1, 1024, time.Minute)
	now := time.Unix(100, 0)
	positive := CompileResult{planTerminal: &Terminal{ReasonCode: "POSITIVE"}}
	negative := CompileResult{planTerminal: &Terminal{ReasonCode: "NEGATIVE"}}
	cache.put("positive", positive, 64, false, now)
	cache.put("negative", negative, 64, true, now)
	if _, ok := cache.get("positive", now); ok {
		t.Fatal("evicted LRU entry remained in cache")
	}
	if _, ok := cache.get("negative", now.Add(time.Minute)); ok {
		t.Fatal("expired negative entry remained in cache")
	}
	stats := cache.snapshot()
	if stats.Evictions != 1 || stats.Entries != 0 || stats.Bytes != 0 || stats.Misses != 2 {
		t.Fatalf("CacheStats = %+v", stats)
	}
}

func TestCompilerCacheKeyIncludesDatasetAndStateSemantics(t *testing.T) {
	compiler := newTestCompiler(t)
	request := validRequest(validPlan())
	if _, err := compiler.Compile(context.Background(), request); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	request.DatasetContract.SchemaDigest = "4" + request.DatasetContract.SchemaDigest[1:]
	if _, err := compiler.Compile(context.Background(), request); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	request.StateSemantics.HistoryCellSemanticsVersion = "detect-history-cell-v2"
	if _, err := compiler.Compile(context.Background(), request); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if stats := compiler.CacheStats(); stats.Misses != 3 || stats.Hits != 0 {
		t.Fatalf("CacheStats() = %+v", stats)
	}
}

func TestCompilerBudgetDigestIncludesTriggerRuntimeBudgets(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Limits)
	}{
		{name: "trigger window", change: func(limits *Limits) { limits.MaxTriggerWindowSize-- }},
		{name: "recovery window", change: func(limits *Limits) { limits.MaxRecoveryConsecutiveWindows-- }},
		{name: "trigger compute", change: func(limits *Limits) { limits.MaxTriggerComputeCost-- }},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := testLimits()
			first, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), base)
			if err != nil {
				t.Fatal(err)
			}
			changed := base
			test.change(&changed)
			second, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), changed)
			if err != nil {
				t.Fatal(err)
			}
			if first.budgetDigest == second.budgetDigest {
				t.Fatal("trigger runtime budget change did not invalidate compiler budget digest")
			}
		})
	}
}

func TestCompilerEnforcesCompiledPlanBudgetAndReportsCost(t *testing.T) {
	compiler := newTestCompiler(t)
	compiled := mustCompilePlan(t, compiler, validPlan())
	estimate := compiled.ResourceEstimate()
	if estimate.CompiledBytes == 0 || estimate.ASTNodes == 0 || estimate.Algorithms != 1 ||
		estimate.FixedComputeCost == 0 || estimate.CostPerRecord == 0 || estimate.StatePointsPerSeries == 0 {
		t.Fatalf("ResourceEstimate() = %+v", estimate)
	}

	limits := testLimits()
	limits.MaxCompiledPlanBytes = 1
	compiler, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), limits)
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	result, err := compiler.Compile(context.Background(), validRequest(validPlan()))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if terminal := result.PlanTerminal(); terminal == nil || terminal.ReasonCode != contract.ReasonPlanBudgetExceeded {
		t.Fatalf("PlanTerminal() = %+v", terminal)
	}
}
