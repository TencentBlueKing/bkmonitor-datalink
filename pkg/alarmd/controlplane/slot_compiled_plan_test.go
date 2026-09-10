package controlplane

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type countingPlanCompiler struct {
	calls    int
	requests []strategy.CompileRequest
	err      error
}

func (compiler *countingPlanCompiler) Compile(
	_ context.Context,
	request strategy.CompileRequest,
) (strategy.CompileResult, error) {
	compiler.calls++
	compiler.requests = append(compiler.requests, request)
	if compiler.err != nil {
		return strategy.CompileResult{}, compiler.err
	}
	return strategy.CompileResult{}, nil
}

func compileKey(plan string, query string) slotCompiledPlanKey {
	return slotCompiledPlanKey{planRevision: plan, queryRevision: execution.QueryRevision(query)}
}

// TestSlotCompiledPlansCompilesOncePerContent proves a Slot does not recompile
// a Plan that did not change, and does recompile one that did. The key is the
// Plan revision with the Query revision it is compiled against, both canonical
// digests of the content they name.
func TestSlotCompiledPlansCompilesOncePerContent(t *testing.T) {
	plans := newSlotCompiledPlans()
	compiler := &countingPlanCompiler{}
	request := strategy.CompileRequest{}
	for slot := 0; slot < 5; slot++ {
		if _, err := plans.compile(context.Background(), compiler, compileKey("plan-r1", "query-r1"), request); err != nil {
			t.Fatalf("compile slot %d: %v", slot, err)
		}
	}
	if compiler.calls != 1 {
		t.Fatalf("five Slots of one unchanged Plan compiled %d times, want 1", compiler.calls)
	}
	if _, err := plans.compile(context.Background(), compiler, compileKey("plan-r2", "query-r1"), request); err != nil {
		t.Fatalf("compile changed plan: %v", err)
	}
	if compiler.calls != 2 {
		t.Fatalf("a changed Plan revision compiled %d times in total, want 2", compiler.calls)
	}
	if _, err := plans.compile(context.Background(), compiler, compileKey("plan-r1", "query-r2"), request); err != nil {
		t.Fatalf("compile changed query: %v", err)
	}
	if compiler.calls != 3 {
		t.Fatalf("a changed Query revision compiled %d times in total, want 3", compiler.calls)
	}
}

// TestSlotCompiledPlansWithoutRevisionsAlwaysCompiles proves an unnamed
// content is never remembered: without both revisions there is nothing that
// says two requests are the same Plan.
func TestSlotCompiledPlansWithoutRevisionsAlwaysCompiles(t *testing.T) {
	plans := newSlotCompiledPlans()
	compiler := &countingPlanCompiler{}
	for _, key := range []slotCompiledPlanKey{compileKey("", "query-r1"), compileKey("plan-r1", ""), {}} {
		for attempt := 0; attempt < 2; attempt++ {
			if _, err := plans.compile(context.Background(), compiler, key, strategy.CompileRequest{}); err != nil {
				t.Fatalf("compile %+v: %v", key, err)
			}
		}
	}
	if compiler.calls != 6 {
		t.Fatalf("unnamed content compiled %d times, want 6", compiler.calls)
	}
}

// TestSlotCompiledPlansDoesNotRememberAFailure proves a compile error belongs
// to the attempt and not to the content, so the next Slot tries again.
func TestSlotCompiledPlansDoesNotRememberAFailure(t *testing.T) {
	plans := newSlotCompiledPlans()
	compiler := &countingPlanCompiler{err: errors.New("compile unavailable")}
	key := compileKey("plan-r1", "query-r1")
	if _, err := plans.compile(context.Background(), compiler, key, strategy.CompileRequest{}); err == nil {
		t.Fatal("a failing compile was reported as successful")
	}
	compiler.err = nil
	if _, err := plans.compile(context.Background(), compiler, key, strategy.CompileRequest{}); err != nil {
		t.Fatalf("compile after failure: %v", err)
	}
	if compiler.calls != 2 {
		t.Fatalf("a failed compile was compiled %d times, want 2", compiler.calls)
	}
	// The successful result is remembered from then on.
	if _, err := plans.compile(context.Background(), compiler, key, strategy.CompileRequest{}); err != nil {
		t.Fatalf("compile after recovery: %v", err)
	}
	if compiler.calls != 2 {
		t.Fatalf("a recovered Plan compiled %d times, want 2", compiler.calls)
	}
}

// TestSlotCompiledPlansStartsOverAtItsBound proves the table stays bounded.
func TestSlotCompiledPlansStartsOverAtItsBound(t *testing.T) {
	plans := newSlotCompiledPlans()
	compiler := &countingPlanCompiler{}
	for index := 0; index < slotCompiledPlanEntries+8; index++ {
		key := compileKey("plan-r"+strconv.Itoa(index), "query-r1")
		if _, err := plans.compile(context.Background(), compiler, key, strategy.CompileRequest{}); err != nil {
			t.Fatalf("compile %d: %v", index, err)
		}
	}
	plans.mu.RLock()
	held := len(plans.results)
	plans.mu.RUnlock()
	if held > slotCompiledPlanEntries {
		t.Fatalf("table holds %d entries, bound is %d", held, slotCompiledPlanEntries)
	}
}
