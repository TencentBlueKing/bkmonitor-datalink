// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// TestProductionPhaseTwoStateGenerationEditRewarmsAndResumes: an edit that
// moves a Plan's state generation - a threshold, or a recovery window now
// that the Level contract inputs are part of the generation - must let the
// Query Group resume: the first Slot on the new Segment completes query-free
// under CONFIG_DRIFT while the forced WARMING marker is written, and the
// Slots after it warm and reach a FULL completion. Two ways this used to go
// wrong, both silent on every completion counter: a recovery window edit
// failed the loaded state's Level contract on every attempt, and any
// generation move made the retried Slot collide with its own WARMING marker
// on every attempt, until the Slot aged out ten minutes later.
func TestProductionPhaseTwoStateGenerationEditRewarmsAndResumes(t *testing.T) {
	edits := []struct {
		name string
		// carries is whether the edit leaves every Level's detection as it
		// was, so the activation carries the history across; requiredPoints
		// is then how many positions the edited window needs, which is what
		// a series short of history still warms up on.
		carries        bool
		requiredPoints uint32
		install        func(t *testing.T, ctx context.Context, fixture *cutoverStallFixture)
	}{
		{name: "recovery window", carries: true, requiredPoints: uint32(cutoverStallTriggerWindow + 3 - 1), install: func(t *testing.T, ctx context.Context, fixture *cutoverStallFixture) {
			installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, func(first map[string]any) {
				for _, detect := range first["detects"].([]any) {
					detect.(map[string]any)["recovery_config"].(map[string]any)["check_window"] = 3
				}
			})
		}},
		// A threshold edit carries too: the results the old threshold decided
		// stand as decided, as Python's check results do.
		{name: "threshold", carries: true, requiredPoints: uint32(cutoverStallTriggerWindow), install: func(t *testing.T, ctx context.Context, fixture *cutoverStallFixture) {
			installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, func(first map[string]any) {
				item := first["items"].([]any)[0].(map[string]any)
				for _, algorithm := range item["algorithms"].([]any) {
					for _, group := range algorithm.(map[string]any)["config"].([]any) {
						for _, condition := range group.([]any) {
							condition.(map[string]any)["threshold"] = 81
						}
					}
				}
			})
		}},
	}
	for _, edit := range edits {
		t.Run(edit.name, func(t *testing.T) {
			fixture := startCutoverFixture(t, nil)
			ctx := context.Background()
			base := fixture.base

			activationBefore, err := fixture.repository.LoadActivation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			generationBefore := activationGenerationOf(t, activationBefore.Plans, "1001")

			edit.install(t, ctx, fixture)
			boundary := execution.EvaluationTime(base + 59)
			fixture.clock.Store(int64(boundary) * 1000)
			for attempt := 0; attempt < 2; attempt++ {
				if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
					t.Fatalf("publication refresh %d error = %v", attempt+1, err)
				}
			}
			activationAfter, err := fixture.repository.LoadActivation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if activationGenerationOf(t, activationAfter.Plans, "1001") == generationBefore {
				t.Fatalf("a %s edit must move the state generation: %s", edit.name, generationBefore)
			}
			var requiredFullSlots uint32
			var carry *execution.StateCarry
			for _, record := range activationAfter.Plans {
				if record.Fact.Plan.StrategyID == "1001" {
					requiredFullSlots = record.Fact.Selected.RequiredFullSlots
					carry = record.Fact.Selected.Carry
				}
			}
			if (carry != nil) != edit.carries {
				t.Fatalf("a %s edit carried %+v, want carried=%t", edit.name, carry, edit.carries)
			}
			// A carried activation warms up behind its guard for one full Slot;
			// a series whose carried history is shorter than the edited window
			// still warms up on its window, one position per Slot. This fixture
			// runs one Slot before the edit, so that is what bounds it.
			warmSlots := requiredFullSlots
			if carry != nil {
				warmSlots = edit.requiredPoints
			}

			var full, drifted bool
			idle := false
			for tick := 1; tick <= 16 && !full; tick++ {
				nextAt := fixture.runner.NextReadyAt()
				at := fixture.now().Add(time.Second)
				if nextAt.After(at) {
					at = nextAt.Add(time.Millisecond)
				}
				if idle {
					// Nothing was due: move to the next grid point, the way the
					// dispatcher would only wake this Runner when its next Slot is.
					next := at.Unix() + 60 - at.Unix()%60
					at = time.Unix(next, 0).Add(time.Second)
				}
				fixture.clock.Store(at.UnixMilli())
				result, attempted, err := fixture.runner.RunOne(ctx)
				idle = !attempted
				if err != nil {
					t.Fatalf("tick %d RunOne error = %v: the edit must re-warm the Plan, not fail its Slots", tick, err)
				}
				t.Logf("tick %d at +%ds: attempted=%t completed=%t result=%s reason=%s kind=%s", tick, at.Unix()-base, attempted, result.Completed, result.Result, result.ReasonCode, result.CompletionKind)
				if result.Completed && result.ReasonCode == execution.ReasonCode(contract.ReasonConfigDrift) {
					drifted = true
				}
				if result.Completed && result.CompletionKind == execution.CompletionFull {
					full = true
				}
			}
			if !drifted || !full {
				t.Fatalf("after a %s edit the Query Group must complete the drift Slot query-free and then reach FULL: drifted=%t full=%t", edit.name, drifted, full)
			}
			// The drift Slot, then one warming Slot per required full Slot, then
			// the first FULL one; anything slower is a Slot aging out, not warming.
			if resumed := fixture.now().Unix() - base; resumed > int64(warmSlots+2)*60+1 {
				t.Fatalf("resuming took %ds with %d warming Slots, which is the shape of a Slot aging out rather than warming", resumed, warmSlots)
			}
			for _, observation := range fixture.observed() {
				if failure := observation.QueryFailure; failure != nil && failure.Code == execution.QueryFailureCodeStateContractMismatch {
					t.Fatalf("the loaded state contract mismatch was reported after the edit: %+v", failure)
				}
				if observation.Stage == observability.StageQueryCompleted && observation.Result == observability.ResultFailed {
					t.Fatalf("a query failure was reported after the edit: %+v", observation.QueryFailure)
				}
			}
		})
	}
}

// TestProductionPhaseTwoACarriedEditResumesWithinOneWarmingSlot: an edit that
// leaves detection alone - here the trigger's required count - carries the
// history the window already holds, so after the drift Slot the Plan warms
// up for one full Slot and is back, instead of warming up its whole window
// again. The fixture runs a full window before the edit, which is what makes
// the difference visible: with no history the Plan warms on its window
// whether it carries or not.
func TestProductionPhaseTwoACarriedEditResumesWithinOneWarmingSlot(t *testing.T) {
	previousWindow := cutoverStallTriggerWindow
	cutoverStallTriggerWindow = 3
	t.Cleanup(func() { cutoverStallTriggerWindow = previousWindow })
	thresholdAt := func(value int) func(first map[string]any) {
		return func(first map[string]any) {
			item := first["items"].([]any)[0].(map[string]any)
			for _, algorithm := range item["algorithms"].([]any) {
				for _, group := range algorithm.(map[string]any)["config"].([]any) {
					for _, condition := range group.([]any) {
						condition.(map[string]any)["threshold"] = value
					}
				}
			}
		}
	}
	threshold := thresholdAt(81)
	// renumber moves every Level to id 2: no Level is common to both
	// versions, so the Plan warms up whole under its activation's guard
	// (none_no_common_level).
	renumber := func(first map[string]any) {
		for _, algorithm := range first["items"].([]any)[0].(map[string]any)["algorithms"].([]any) {
			algorithm.(map[string]any)["level"] = 2
		}
		for _, detect := range first["detects"].([]any) {
			detect.(map[string]any)["level"] = 2
		}
	}
	both := func(edits ...func(first map[string]any)) func(first map[string]any) {
		return func(first map[string]any) {
			for _, edit := range edits {
				edit(first)
			}
		}
	}
	for _, edit := range []struct {
		name string
		// outcome is the series carry outcome the edit's series report.
		outcome string
		change  func(first map[string]any)
		// fullsBefore is how many FULL Slots run before the edit; the window
		// fills on the cutoverStallTriggerWindow-th.
		fullsBefore int
		// preludes are edits made, in order, before the edit, each followed
		// by its FULL Slots; every later change includes the earlier ones.
		preludes []editStep
	}{
		{name: "trigger count", outcome: "carried", fullsBefore: cutoverStallTriggerWindow, change: func(first map[string]any) {
			for _, detect := range first["detects"].([]any) {
				detect.(map[string]any)["trigger_config"].(map[string]any)["count"] = 2
			}
		}},
		// The results the old threshold decided stand as decided and are
		// carried, as Python's are.
		{name: "threshold", outcome: "carried_across_detect", fullsBefore: cutoverStallTriggerWindow, change: threshold},
		// The window had not filled when the edit landed: its Level was
		// WARMING for its own holes, which come along as holes, and what it
		// had decided is kept. Skipping it started the series over and made
		// the edit wait a whole window more (as for a
		// probe edited minutes after it first alerted).
		{name: "threshold before the window filled", outcome: "carried_across_detect", fullsBefore: cutoverStallTriggerWindow - 1, change: threshold},
		// A Plan an activation warmed up whole is held by that activation's
		// own guard (CONFIG_DRIFT) until its trigger and recovery windows
		// converge, not by its own holes: the positions it guards are before
		// the Plan ran, absent points. An edit inside that stretch keeps what
		// the Plan had decided, as Python does (as for a
		// probe edited minutes after it was created).
		{name: "threshold inside an activation's warming", outcome: "carried_across_detect", fullsBefore: cutoverStallTriggerWindow,
			preludes: []editStep{{change: renumber, fulls: 1}}, change: both(renumber, threshold)},
		// Two edits inside it: the second activation's guard holds the
		// window again, and the third version still carries.
		{name: "two threshold edits inside an activation's warming", outcome: "carried_across_detect", fullsBefore: cutoverStallTriggerWindow,
			preludes: []editStep{{change: renumber, fulls: 1}, {change: both(renumber, threshold), fulls: 1}}, change: both(renumber, thresholdAt(82))},
	} {
		t.Run(edit.name, func(t *testing.T) {
			carriedEditResumesWithinOneWarmingSlot(t, edit.change, edit.outcome, edit.fullsBefore, edit.preludes...)
		})
	}
}

// editStep is one edit made before the measured one, and the FULL Slots run
// after it.
type editStep struct {
	change func(first map[string]any)
	fulls  int
}

func carriedEditResumesWithinOneWarmingSlot(t *testing.T, change func(first map[string]any), outcome string, fullsBefore int, preludes ...editStep) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	idle := false
	// drive runs Slots the way the dispatcher would until done says so, and
	// reports whether it got there within the ticks it was given.
	drive := func(ticks int, done func(execution.SlotExecutionResult) bool) bool {
		for tick := 1; tick <= ticks; tick++ {
			nextAt := fixture.runner.NextReadyAt()
			at := fixture.now().Add(time.Second)
			if nextAt.After(at) {
				at = nextAt.Add(time.Millisecond)
			}
			if idle {
				next := at.Unix() + 60 - at.Unix()%60
				at = time.Unix(next, 0).Add(time.Second)
			}
			fixture.clock.Store(at.UnixMilli())
			result, attempted, err := fixture.runner.RunOne(ctx)
			idle = !attempted
			if err != nil {
				t.Fatalf("tick %d RunOne error = %v", tick, err)
			}
			t.Logf("at +%ds: attempted=%t completed=%t reason=%s kind=%s", at.Unix()-fixture.base, attempted, result.Completed, result.ReasonCode, result.CompletionKind)
			if result.Completed && done(result) {
				return true
			}
		}
		return false
	}
	full := func(result execution.SlotExecutionResult) bool {
		return result.CompletionKind == execution.CompletionFull
	}
	// fullsBefore FULL Slots of history: a full window, or one short of it.
	fulls := 0
	if !drive(40, func(result execution.SlotExecutionResult) bool {
		if full(result) {
			fulls++
		}
		return fulls >= fullsBefore
	}) {
		t.Fatalf("the Plan did not reach %d FULL Slots before the edit", fullsBefore)
	}
	for index, step := range preludes {
		installEditedStrategies(t, ctx, fixture.redisClient, 1725000300+int64(index)*60, step.change)
		fixture.clock.Store((fixture.now().Unix()/60*60 + 59) * 1000)
		for attempt := 0; attempt < 2; attempt++ {
			if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
				t.Fatalf("prelude %d refresh %d error = %v", index+1, attempt+1, err)
			}
		}
		idle, fulls = false, 0
		if !drive(16, func(result execution.SlotExecutionResult) bool {
			if full(result) {
				fulls++
			}
			return fulls >= step.fulls
		}) {
			t.Fatalf("the Plan did not run %d FULL Slots after prelude %d", step.fulls, index+1)
		}
	}

	before := len(fixture.observed())
	activationBefore, err := fixture.repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	generationBefore := activationGenerationOf(t, activationBefore.Plans, "1001")
	installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, change)
	boundary := fixture.now().Unix()/60*60 + 59
	fixture.clock.Store(boundary * 1000)
	for attempt := 0; attempt < 2; attempt++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("publication refresh %d error = %v", attempt+1, err)
		}
	}
	activation, err := fixture.repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var carriedLevels []uint32
	for _, record := range activation.Plans {
		if record.Fact.Plan.StrategyID == "1001" && (record.Fact.Selected.Carry == nil || record.Fact.Selected.RequiredFullSlots != 1) {
			t.Fatalf("the edit did not carry: %+v", record.Fact.Selected)
		}
		if record.Fact.Plan.StrategyID == "1001" {
			carriedLevels = record.Fact.Selected.Carry.Levels
		}
	}
	idle = false
	if !drive(16, full) {
		t.Fatal("the Plan did not come back to FULL after the edit")
	}
	// The drift Slot, one warming Slot, then FULL on the carried window.
	if resumed := fixture.now().Unix() - boundary; resumed > int64(1+2)*60+1 {
		t.Fatalf("resuming took %ds after the edit, want the drift Slot, one warming Slot and a FULL one", resumed)
	}
	// The one warming Slot is a guard's, and a Slot completes FULL whatever
	// its series' windows hold; what says the history was carried is the
	// windows themselves. The first window read after the edit holds the
	// carried points and this Slot's own - short only by the position of the
	// drift Slot, which completed without a query and wrote none - where a
	// series that had started over would hold this Slot's one point alone.
	carried, summarised := 0, 0
	for _, observation := range fixture.observed()[before:] {
		if facts := observation.StateCarry; facts != nil && facts.Scope == "series" {
			if facts.Result != outcome {
				t.Fatalf("a series carried as %s, want %s", facts.Result, outcome)
			}
			carried += facts.Count
		}
		if coverage := observation.HistoryCoverage; coverage != nil && coverage.Levels > 0 {
			summarised++
			if coverage.WorstValid <= 1 {
				t.Fatalf("a window after the edit held %d of %d positions: the series started over instead of carrying", coverage.WorstValid, coverage.WorstRequired)
			}
		}
	}
	if carried == 0 || summarised == 0 {
		t.Fatalf("carried=%d series, summarised=%d windows after the edit: want the series to carry and their windows to be read", carried, summarised)
	}
	// object.get names where the Plan's state comes from: the generation
	// before the edit, found in the timeline, and since when the new one runs.
	// The fixture's strategies carry no snapshot revision.
	generationAfter := activationGenerationOf(t, activation.Plans, "1001")
	var found *controlplane.PlanCarryFact
	var foundIn execution.QueryGroupIdentity
	groups := fixture.bundle.ownedQueryGroups()
	for _, group := range groups {
		recorder := httptest.NewRecorder()
		fixture.bundle.dependencies.FleetAPI.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/objects/"+string(group), nil))
		var body struct {
			PlanCarries *controlplane.PlanCarriesReading `json:"plan_carries"`
			Unavailable string                           `json:"plan_carries_unavailable"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode the object detail: %v (%s)", err, recorder.Body.String())
		}
		if body.PlanCarries == nil {
			t.Fatalf("object %s names no plan carries (unavailable %q)", group, body.Unavailable)
		}
		for index, plan := range body.PlanCarries.Plans {
			if plan.Plan.StrategyID == "1001" {
				found, foundIn = &body.PlanCarries.Plans[index], group
			}
		}
	}
	if found == nil || found.StateGeneration != generationAfter || found.ActivatedAt < execution.EvaluationTime(boundary) ||
		found.CarriedFrom == nil || found.CarriedFrom.StateGeneration != generationBefore || !found.CarriedFrom.Found ||
		len(carriedLevels) != 1 || !slices.Equal(found.CarriedFrom.Levels, carriedLevels) {
		t.Fatalf("object.get says %+v (carried from %+v), want generation %s activated at or after %d, carried from %s and found, Levels %v",
			found, found.CarriedFrom, generationAfter, boundary, generationBefore, carriedLevels)
	}
	// slot.query reads the same source as object.get, not a reading of its
	// own.
	reading, err := cliPlanCarries(fixture.repository)(context.Background(), foundIn)
	if err != nil || !slices.ContainsFunc(reading.Plans, func(fact controlplane.PlanCarryFact) bool { return reflect.DeepEqual(fact, *found) }) {
		t.Fatalf("slot.query's plan carries read %+v (%v), want the Plan object.get names, %+v", reading, err, *found)
	}
	if got := counterValue(t, fixture.bundle.dependencies.Recorder, "alarmd_fleet_plan_carries_reads_total", map[string]string{"result": "read"}); got != float64(len(groups)) {
		t.Fatalf("plan carries reads counted %v, want one read for each of the %d objects asked", got, len(groups))
	}
}

func activationGenerationOf(t *testing.T, records []controlplane.PlanActivationRecord, strategyID string) execution.StateGeneration {
	t.Helper()
	for _, record := range records {
		if record.Fact.Plan.StrategyID == strategyID {
			return record.Fact.Selected.StateGeneration
		}
	}
	t.Fatalf("strategy %s has no activation record", strategyID)
	return ""
}

// installEditedStrategies stores the two cutover fixture strategies with an
// edit applied to the first one, everything else as
// installCutoverStallStrategies stores it for the initial publication.
func installEditedStrategies(t *testing.T, ctx context.Context, redisClient *redis.Client, updateTime int64, edit func(first map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]any
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}
	first["update_time"] = updateTime
	firstItem := first["items"].([]any)[0].(map[string]any)
	firstItem["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 60
	for _, detect := range first["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = cutoverStallTriggerWindow
	}
	// The second strategy is derived before the edit so that only the first
	// one changes.
	unedited, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	edit(first)
	firstEncoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var second map[string]any
	if err := json.Unmarshal(unedited, &second); err != nil {
		t.Fatal(err)
	}
	second["id"] = 1002
	item := second["items"].([]any)[0].(map[string]any)
	item["id"], item["query_md5"] = 12, "cutover-stall-system.mem"
	item["query_configs"].([]any)[0].(map[string]any)["result_table_id"] = "system.mem"
	secondEncoded, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":  `[1001,1002]`,
		"alarm-config.strategy_1001": firstEncoded,
		"alarm-config.strategy_1002": secondEncoded,
	} {
		if err := redisClient.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}
