// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// The word every assertion below expects, written by hand rather than read
// from the implementation's constant.
const wantCompileFailedReason = "RUNTIME_PLAN_COMPILE_FAILED"

// compileIsolationPlan is one strategy at one document revision: a threshold
// Plan whose revision is the only thing two versions differ by.
func compileIsolationPlan(t *testing.T, strategyID, businessID, revision string) FrozenPlan {
	t.Helper()
	plan := runtimeClosureNamedFrozenPlan(t, strategyID, businessID,
		[]contract.LevelIRV2{runtimeClosureLevel(1, strategy.DetectorKindThreshold)})
	plan.Plan.StrategyRef.Revision = revision
	plan.Plan.StrategyIR.StrategyRef.Revision = revision
	plan.PlanRevision = runtimeClosurePlanRevision(t, plan.Plan)
	return plan
}

// compileIsolationCompiler is the real compiler, except that it fails with err
// for every document in fails, by strategy and revision.
func compileIsolationCompiler(t *testing.T, err error, fails ...string) (RuntimePlanCompiler, strategy.StateSemantics) {
	t.Helper()
	underlying, semantics := runtimeClosureCompiler(t)
	return runtimeClosureCompilerFunc(func(ctx context.Context, request strategy.CompileRequest) (strategy.CompileResult, error) {
		if slices.Contains(fails, request.Plan.PlanID+"@"+request.Plan.StrategyRef.Revision) {
			return strategy.CompileResult{}, err
		}
		return underlying.Compile(ctx, request)
	}), semantics
}

func compileIsolationRunning(catalog Catalog) map[string]string {
	running := make(map[string]string)
	for _, group := range catalog.QueryGroups {
		for _, plan := range group.Plans {
			running[plan.Identity.StrategyID] = plan.Plan.StrategyRef.Revision
		}
	}
	return running
}

func compileIsolationPlanDispositions(catalog Catalog, strategyID string) []ObjectDisposition {
	var found []ObjectDisposition
	for _, disposition := range catalog.Dispositions {
		if disposition.SourceID == strategyID && disposition.Scope == "PLAN" {
			found = append(found, disposition)
		}
	}
	return found
}

// One strategy whose compile returns an error keeps running the Plan it last
// ran, and every other strategy's change of the same round takes effect.
func TestOneStrategysCompileErrorDoesNotHoldBackTheOthersChanges(t *testing.T) {
	compiler, semantics := compileIsolationCompiler(t, errors.New("algorithm compiler panicked on its config"), "1001@r2")
	first := runtimeClosureCatalog(
		runtimeClosureQueryGroup(t, "1", compileIsolationPlan(t, "1001", "1", "r1"), compileIsolationPlan(t, "1002", "1", "r1")),
		runtimeClosureQueryGroup(t, "2", compileIsolationPlan(t, "2001", "2", "r1")),
	)
	published, err := retainRuntimeExecutableCatalog(context.Background(), first, nil, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}

	next := runtimeClosureCatalog(
		runtimeClosureQueryGroup(t, "1", compileIsolationPlan(t, "1001", "1", "r2"), compileIsolationPlan(t, "1002", "1", "r2")),
		runtimeClosureQueryGroup(t, "2", compileIsolationPlan(t, "2001", "2", "r2")),
	)
	built, err := retainRuntimeExecutableCatalog(context.Background(), next,
		&PublishedSnapshot{QueryGroups: published.QueryGroups}, compiler, semantics)
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v; one strategy's compile error held every strategy's change back", err)
	}

	running := compileIsolationRunning(built)
	want := map[string]string{"1001": "r1", "1002": "r2", "2001": "r2"}
	for strategyID, revision := range want {
		if running[strategyID] != revision {
			t.Fatalf("running = %v, want %v: the failing strategy on its last Plan and the others on their new one", running, want)
		}
	}
	dispositions := compileIsolationPlanDispositions(built, "1001")
	if len(dispositions) != 1 || dispositions[0].Disposition != DispositionStaleConfig ||
		dispositions[0].Reason != wantCompileFailedReason || !strings.Contains(dispositions[0].Detail, "panicked") {
		t.Fatalf("1001 dispositions = %+v, want one STALE_CONFIG %s carrying the compiler's error", dispositions, wantCompileFailedReason)
	}
	for _, strategyID := range []string{"1002", "2001"} {
		got := compileIsolationPlanDispositions(built, strategyID)
		if len(got) != 1 || got[0].Disposition != DispositionAccepted {
			t.Fatalf("%s dispositions = %+v, want ACCEPTED", strategyID, got)
		}
	}
	composition := ComposeCatalog(built)
	if composition.Withheld[WithheldKey{Disposition: DispositionStaleConfig, Reason: wantCompileFailedReason}] != 1 {
		t.Fatalf("withheld = %+v, want the failing strategy counted once", composition.Withheld)
	}
}

// A strategy whose compile returns an error and that has no Plan to fall back
// on is out of the Catalog, and stays a strategy the source lists: it is named
// and counted, never read as gone or refused for a capability, so nothing
// closes its alerts on the strength of its absence.
func TestAStrategyThatNeverCompiledIsNamedAndNotReadAsGone(t *testing.T) {
	compiler, semantics := compileIsolationCompiler(t, errors.New("derive state compatibility: boom"), "1001@r1")
	catalog := runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1",
		compileIsolationPlan(t, "1001", "1", "r1"), compileIsolationPlan(t, "1002", "1", "r1")))

	built, err := retainRuntimeExecutableCatalog(context.Background(), catalog, nil, compiler, semantics)
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v; one strategy's compile error took the whole round", err)
	}
	running := compileIsolationRunning(built)
	if _, ok := running["1001"]; ok || running["1002"] != "r1" || len(running) != 1 {
		t.Fatalf("running = %v, want only 1002", running)
	}
	dispositions := compileIsolationPlanDispositions(built, "1001")
	if len(dispositions) != 1 || dispositions[0].Disposition != DispositionConfigRejected ||
		dispositions[0].Reason != wantCompileFailedReason || dispositions[0].Detail == "" {
		t.Fatalf("1001 dispositions = %+v, want one CONFIG_REJECTED %s with the error", dispositions, wantCompileFailedReason)
	}

	composition := ComposeCatalog(built)
	if !slices.Contains(composition.ListedStrategies, "1001") {
		t.Fatalf("listed = %v: a strategy the source lists reads as gone", composition.ListedStrategies)
	}
	if composition.Withheld[WithheldKey{Disposition: DispositionConfigRejected, Reason: wantCompileFailedReason}] != 1 {
		t.Fatalf("withheld = %+v, want the failing strategy counted once", composition.Withheld)
	}
	reconciler := &SourceReconciler{}
	reconciler.strategies.replace(buildStrategyIndex(SnapshotPublicationRef{SnapshotRevision: built.SnapshotRevision},
		built.QueryGroups, built.Dispositions))
	if refused := reconciler.CapabilityRefused(); refused == nil {
		t.Fatal("no capability answer at all from a held publication")
	} else if _, closes := refused["1001"]; closes {
		t.Fatalf("capability refused = %v: a compile error would close the strategy's alerts", refused)
	}
	if lookup := reconciler.LookupStrategy("1001"); !lookup.Found || len(lookup.Plans) != 0 {
		t.Fatalf("lookup = %+v, want the strategy found by its disposition and running nothing", lookup)
	}
}

// The strategy's last Plan failing to compile as well leaves it out of the
// Catalog under the same name, rather than failing the round.
func TestACompileErrorWithAnUncompilableLastPlanIsStillOneStrategys(t *testing.T) {
	ok, semantics := compileIsolationCompiler(t, nil)
	first := runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1",
		compileIsolationPlan(t, "1001", "1", "r1"), compileIsolationPlan(t, "1002", "1", "r1")))
	published, err := retainRuntimeExecutableCatalog(context.Background(), first, nil, ok, semantics)
	if err != nil {
		t.Fatal(err)
	}
	failing, _ := compileIsolationCompiler(t, errors.New("boom"), "1001@r1", "1001@r2")
	next := runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1",
		compileIsolationPlan(t, "1001", "1", "r2"), compileIsolationPlan(t, "1002", "1", "r2")))
	built, err := retainRuntimeExecutableCatalog(context.Background(), next,
		&PublishedSnapshot{QueryGroups: published.QueryGroups}, failing, semantics)
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
	}
	if running := compileIsolationRunning(built); len(running) != 1 || running["1002"] != "r2" {
		t.Fatalf("running = %v, want only 1002 on r2", running)
	}
	dispositions := compileIsolationPlanDispositions(built, "1001")
	if len(dispositions) != 1 || dispositions[0].Disposition != DispositionConfigRejected || dispositions[0].Reason != wantCompileFailedReason {
		t.Fatalf("1001 dispositions = %+v, want one CONFIG_REJECTED %s", dispositions, wantCompileFailedReason)
	}
}

// A failure that is not one strategy's still holds the whole round back, so
// the Catalog already published stays in force for everyone.
func TestACompileFailureNotAboutOneStrategyStillHoldsTheRound(t *testing.T) {
	t.Run("the round's context ended", func(t *testing.T) {
		compiler, semantics := runtimeClosureCompiler(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		catalog := runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1",
			compileIsolationPlan(t, "1001", "1", "r1"), compileIsolationPlan(t, "1002", "1", "r1")))
		if _, err := retainRuntimeExecutableCatalog(ctx, catalog, nil, compiler, semantics); !errors.Is(err, context.Canceled) {
			t.Fatalf("retainRuntimeExecutableCatalog() error = %v, want the round held on its context", err)
		}
	})
	t.Run("one strategy's compile met a deadline", func(t *testing.T) {
		compiler, semantics := compileIsolationCompiler(t, context.DeadlineExceeded, "1001@r1")
		catalog := runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1",
			compileIsolationPlan(t, "1001", "1", "r1"), compileIsolationPlan(t, "1002", "1", "r1")))
		if _, err := retainRuntimeExecutableCatalog(context.Background(), catalog, nil, compiler, semantics); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("retainRuntimeExecutableCatalog() error = %v, want the round held on the deadline", err)
		}
	})
	t.Run("every strategy's compile failed", func(t *testing.T) {
		want := errors.New("compiler unavailable")
		compiler, semantics := compileIsolationCompiler(t, want, "1001@r1", "1002@r1", "2001@r1")
		catalog := runtimeClosureCatalog(
			runtimeClosureQueryGroup(t, "1", compileIsolationPlan(t, "1001", "1", "r1"), compileIsolationPlan(t, "1002", "1", "r1")),
			runtimeClosureQueryGroup(t, "2", compileIsolationPlan(t, "2001", "2", "r1")),
		)
		if _, err := retainRuntimeExecutableCatalog(context.Background(), catalog, nil, compiler, semantics); !errors.Is(err, want) {
			t.Fatalf("retainRuntimeExecutableCatalog() error = %v, want the round held when no strategy compiled", err)
		}
	})
}

// Both of the word's pairs are published at zero, so "no strategy's compile
// failed" is a reading and not an absent series.
func TestTheCompileFailedPairsArePublishedAtZero(t *testing.T) {
	composition := ComposeCatalog(Catalog{Dispositions: []ObjectDisposition{
		{SourceID: "1", Scope: "PLAN", Disposition: DispositionAccepted},
	}})
	for _, disposition := range []Disposition{DispositionStaleConfig, DispositionConfigRejected} {
		key := WithheldKey{Disposition: disposition, Reason: wantCompileFailedReason}
		if count, published := composition.Withheld[key]; !published || count != 0 {
			t.Fatalf("Withheld[%+v] = %d, %v; want a published zero", key, count, published)
		}
	}
}

// compileIsolationTwoLevelPlan is compileIsolationPlan with two threshold
// Levels; spoiled gives Level 2 a connector the compiler refuses, so that
// Level alone is rejected and supplemented from the last good definition.
func compileIsolationTwoLevelPlan(t *testing.T, strategyID, businessID, revision string, spoiled bool) FrozenPlan {
	t.Helper()
	plan := runtimeClosureNamedFrozenPlan(t, strategyID, businessID, []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold), runtimeClosureLevel(2, strategy.DetectorKindThreshold)})
	if spoiled {
		plan.Plan.StrategyIR.Levels[1].Connector = "INVALID"
	}
	plan.Plan.StrategyRef.Revision = revision
	plan.Plan.StrategyIR.StrategyRef.Revision = revision
	plan.PlanRevision = runtimeClosurePlanRevision(t, plan.Plan)
	return plan
}

// A strategy one of whose Levels is refused has that Level supplemented
// from its last good definition, and the supplemented Plan is compiled once
// more to verify it. That verify compile returning an error is the one
// strategy's as well: filed against it under the compile-failed word, with
// the refused Level beside it, out of the Catalog, and every other
// strategy's change of the round published.
func TestASupplementedPlansVerifyCompileErrorIsStillOneStrategys(t *testing.T) {
	underlying, semantics := runtimeClosureCompiler(t)
	verified := 0
	compiler := runtimeClosureCompilerFunc(func(ctx context.Context, request strategy.CompileRequest) (strategy.CompileResult, error) {
		// The verify compile: the round's revision with every Level's
		// connector valid again, which only the supplement produces.
		if request.Plan.PlanID == "1001" && request.Plan.StrategyRef.Revision == "r2" {
			spoiled := false
			for _, level := range request.Plan.StrategyIR.Levels {
				spoiled = spoiled || level.Connector == "INVALID"
			}
			if !spoiled {
				verified++
				return strategy.CompileResult{}, errors.New("verify: supplemented level does not compile")
			}
		}
		return underlying.Compile(ctx, request)
	})
	first := runtimeClosureCatalog(
		runtimeClosureQueryGroup(t, "1", compileIsolationTwoLevelPlan(t, "1001", "1", "r1", false), compileIsolationPlan(t, "1002", "1", "r1")),
		runtimeClosureQueryGroup(t, "2", compileIsolationPlan(t, "2001", "2", "r1")),
	)
	published, err := retainRuntimeExecutableCatalog(context.Background(), first, nil, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	next := runtimeClosureCatalog(
		runtimeClosureQueryGroup(t, "1", compileIsolationTwoLevelPlan(t, "1001", "1", "r2", true), compileIsolationPlan(t, "1002", "1", "r2")),
		runtimeClosureQueryGroup(t, "2", compileIsolationPlan(t, "2001", "2", "r2")),
	)
	built, err := retainRuntimeExecutableCatalog(context.Background(), next,
		&PublishedSnapshot{QueryGroups: published.QueryGroups}, compiler, semantics)
	if verified == 0 {
		t.Fatal("setup: the supplemented Plan was never verified; the case does not reach the path")
	}
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v; one strategy's verify compile error held every strategy's change back", err)
	}
	running := compileIsolationRunning(built)
	if _, ok := running["1001"]; ok || running["1002"] != "r2" || running["2001"] != "r2" {
		t.Fatalf("running = %v, want 1001 out and the others on r2", running)
	}
	dispositions := compileIsolationPlanDispositions(built, "1001")
	if len(dispositions) != 1 || dispositions[0].Disposition != DispositionConfigRejected ||
		dispositions[0].Reason != wantCompileFailedReason || !strings.Contains(dispositions[0].Detail, "supplemented") {
		t.Fatalf("1001 plan dispositions = %+v, want one CONFIG_REJECTED %s carrying the verify error", dispositions, wantCompileFailedReason)
	}
	refusedLevel := false
	for _, disposition := range built.Dispositions {
		refusedLevel = refusedLevel || (disposition.SourceID == "1001" && disposition.Scope == "LEVEL" && disposition.LevelID == 2)
	}
	if !refusedLevel {
		t.Fatalf("dispositions = %+v, want the refused Level 2 named beside the compile failure", built.Dispositions)
	}
}
