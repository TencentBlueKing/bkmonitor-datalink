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
	"fmt"
	"testing"
)

// A content key already seen is answered with the cache key deriving it
// gives, without deriving it: the same key, and no allocation. The compile
// through it is the compile the Plan has.
func TestARememberedContentKeyIsTheCacheKeyItsPlanDerives(t *testing.T) {
	compiler := newTestCompiler(t)
	request := validRequest(validPlan())
	want, err := compiler.deriveCompileCacheKey(request)
	if err != nil {
		t.Fatal(err)
	}
	request.ContentKey = "object\x00context\x00default\x002\x001001"
	for round := 0; round < 2; round++ {
		if key, err := compiler.compileCacheKey(request); err != nil || key != want {
			t.Fatalf("round %d: cache key = %q, %v, want the Plan's own %q", round, key, err, want)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if _, err := compiler.compileCacheKey(request); err != nil {
			t.Fatal(err)
		}
	}); allocations != 0 {
		t.Fatalf("a remembered content key allocates %.0f times, want 0: its key was derived again", allocations)
	}
	keyed, err := compiler.Compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ContentKey = ""
	plain, err := compiler.Compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	keyedPlan, _ := keyed.Plan()
	plainPlan, _ := plain.Plan()
	if keyedPlan == nil || keyedPlan != plainPlan {
		t.Fatal("a Plan compiled through its content key is not the one compiled from the Plan itself")
	}
}

// Every input of the compile that is not named by the content key moves the
// cache key on its own: the state semantics, field by field, and the
// compiler's budgets. What the content key names -- the objects and the Plan
// they hold -- moves it by being another content key.
func TestEveryCompileInputOutsideTheContentKeyStillMovesTheCacheKey(t *testing.T) {
	compiler := newTestCompiler(t)
	base := validRequest(validPlan())
	base.ContentKey = "object\x00context\x00default\x002\x001001"
	baseKey, err := compiler.compileCacheKey(base)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, request CompileRequest, on *PlanCompiler) {
		t.Helper()
		got, err := on.compileCacheKey(request)
		if err != nil {
			t.Fatal(err)
		}
		want, err := on.deriveCompileCacheKey(request)
		if err != nil {
			t.Fatal(err)
		}
		if got != want || got == baseKey {
			t.Fatalf("%s: cache key %q, want its own %q, not the remembered %q", name, got, want, baseKey)
		}
	}
	states := map[string]func(*StateSemantics){
		"state schema":  func(s *StateSemantics) { s.StateSchemaVersion += "-next" },
		"codec":         func(s *StateSemantics) { s.CodecSemanticsVersion += "-next" },
		"identity":      func(s *StateSemantics) { s.IdentitySchemaDigest = "4" + s.IdentitySchemaDigest[1:] },
		"source time":   func(s *StateSemantics) { s.SourceTimeSemanticsVersion += "-next" },
		"history cells": func(s *StateSemantics) { s.HistoryCellSemanticsVersion += "-next" },
	}
	for name, change := range states {
		request := base
		change(&request.StateSemantics)
		check(name, request, compiler)
	}

	changed := validPlan()
	changed.StrategyIR.ExecutionSemantics.EvaluationInterval *= 2
	another := validRequest(changed)
	another.ContentKey = "another-object\x00context\x00default\x002\x001001"
	check("another content key", another, compiler)

	limits := testLimits()
	limits.BudgetRevision = "test-budget-v2"
	budgeted, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), limits)
	if err != nil {
		t.Fatal(err)
	}
	check("another budget", base, budgeted)
}

// Past its bound the memory is cleared, never grown, and a key remembered
// again afterwards is still the Plan's own.
func TestTheContentKeyMemoryStaysWithinItsBound(t *testing.T) {
	compiler := newTestCompiler(t)
	request := validRequest(validPlan())
	want, err := compiler.deriveCompileCacheKey(request)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < compileKeyMemoEntries+10; index++ {
		request.ContentKey = fmt.Sprintf("object-%d", index)
		if key, err := compiler.compileCacheKey(request); err != nil || key != want {
			t.Fatalf("content key %d: %q, %v", index, key, err)
		}
		compiler.keys.mu.RLock()
		size := len(compiler.keys.keys)
		compiler.keys.mu.RUnlock()
		if size > compileKeyMemoEntries {
			t.Fatalf("the memory holds %d keys, past its bound of %d", size, compileKeyMemoEntries)
		}
	}
	request.ContentKey = "object-0"
	if key, err := compiler.compileCacheKey(request); err != nil || key != want {
		t.Fatalf("a key cleared and asked again = %q, %v, want %q", key, err, want)
	}
}
