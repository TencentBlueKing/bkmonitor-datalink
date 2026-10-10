package controlplane_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A writer may carry keys of its own next to the ones the platform reads, such
// as a calendar list of its own in every detect's uptime. Python reads only
// time_ranges, calendars and active_calendars there
// (alarm_backends/core/control/strategy.py in_alarm_time) and runs the
// strategy. Refusing the key took every Level of such a strategy out as
// LEVEL_INVALID at compile time, after the Catalog had accepted it.
func TestAnUptimeKeyThePlatformDoesNotReadLeavesTheLevelAsItWas(t *testing.T) {
	calendar := func(id int) map[string]any {
		return map[string]any{"id": id, "bk_tenant_id": "tenant-a", "status": "PRESENT", "items": []any{map[string]any{"id": id + 100, "time_kind": "UNIX_SECONDS", "start_time": 1700000000, "end_time": 1700000300, "time_zone": "UTC", "parent_id": 0, "repeat": map[string]any{}}}}
	}
	build := func(uptime string) controlplane.QueryGroup {
		t.Helper()
		document := map[string]any{}
		if err := json.Unmarshal(realThresholdDocuments(t)[0], &document); err != nil {
			t.Fatal(err)
		}
		for _, raw := range document["detects"].([]any) {
			raw.(map[string]any)["trigger_config"].(map[string]any)["uptime"] = json.RawMessage(uptime)
		}
		document["effective_time_snapshot"] = map[string]any{"schema_version": 1, "status": "READY", "business_timezone": "UTC", "calendars": []any{calendar(7), calendar(9)}}
		encoded, _ := json.Marshal(document)
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: encoded, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
		if err != nil {
			t.Fatal(err)
		}
		if len(catalog.QueryGroups) != 1 {
			t.Fatalf("catalog=%+v", catalog)
		}
		return catalog.QueryGroups[0]
	}
	// Written in the order a writer writes it, which is not the order a
	// re-encoding would give.
	const read = `"time_ranges":[{"start":"09:00","end":"18:00"}],"calendars":[7],"active_calendars":[9]`
	plain := build(`{` + read + `}`)
	for name, extra := range map[string]string{"empty": `[]`, "calendar ids": `[7]`} {
		t.Run(name, func(t *testing.T) {
			carried := build(`{` + read + `,"own_calendars":` + extra + `}`)
			want := compileEveryLevel(t, plain)
			got := compileEveryLevel(t, carried)
			if got.StateCompatibilityHash() != want.StateCompatibilityHash() {
				t.Fatal("a key the platform does not read changed how the strategy runs")
			}
			if !reflect.DeepEqual(compiledFrom(t, carried), compiledFrom(t, plain)) {
				t.Fatal("the plan differs from the one without the key")
			}
		})
	}
	// An uptime with nothing else in it reaches the plan as it was written,
	// so the plans already published do not change.
	t.Run("nothing else in it", func(t *testing.T) {
		trigger := string(plain.Plans[0].Plan.StrategyIR.Levels[0].TriggerPlan.Config)
		if !strings.Contains(trigger, `"uptime":{`+read+`}`) {
			t.Fatalf("uptime was rewritten: %s", trigger)
		}
	})
	// Python fails on a non-empty uptime without time_ranges (KeyError), so
	// one holding nothing but a key it passes over still does not run.
	t.Run("nothing the platform reads", func(t *testing.T) {
		carried := build(`{"own_calendars":[7]}`)
		result := evaluationCoreResult(t, carried.Plans[0].Plan, carried.QueryPlan.Normalization.DatasetContract)
		if terminals := result.LevelTerminals(); len(terminals) == 0 {
			t.Fatal("an uptime Python fails on was run")
		}
	})
}

// The same holds for the history-comparison algorithms: Python reads each
// one's parameters by name (bkmonitor/strategy/serializers.py) and passes over
// anything else in the config.
func TestAComparisonConfigKeyThePlatformDoesNotReadLeavesTheLevelAsItWas(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	build := func(kind string, config map[string]any) controlplane.QueryGroup {
		t.Helper()
		document := g4LegacyStrategyDocument(t, 300, kind, "latency", "custom.application", []string{"service"}, config)
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "300", Document: document, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}}, Planner: planner})
		if err != nil {
			t.Fatal(err)
		}
		if len(catalog.QueryGroups) != 1 {
			t.Fatalf("catalog=%+v", catalog)
		}
		return catalog.QueryGroups[0]
	}
	for kind, config := range map[string]map[string]any{
		strategy.DetectorKindAdvancedYearRound:  {"ceil": 20, "ceil_interval": 2, "fetch_type": "avg"},
		strategy.DetectorKindRingRatioAmplitude: {"ratio": 1, "shock": 2, "threshold": 3},
	} {
		t.Run(kind, func(t *testing.T) {
			carriedConfig := map[string]any{"hover": false, "algorithmUnit": "%"}
			for key, value := range config {
				carriedConfig[key] = value
			}
			plain, carried := build(kind, config), build(kind, carriedConfig)
			want := compileEveryLevel(t, plain)
			got := compileEveryLevel(t, carried)
			if got.StateCompatibilityHash() != want.StateCompatibilityHash() {
				t.Fatal("a key the platform does not read changed how the strategy runs")
			}
			if !reflect.DeepEqual(compiledFrom(t, carried), compiledFrom(t, plain)) {
				t.Fatal("the plan differs from the one without the key")
			}
		})
	}
}

// compiledFrom is what the published plan hands the compiler, decoded, so
// that key order does not count. The plan also carries the source document as
// it came, for the output the platform writes (legacy_output), and that keeps
// every key.
func compiledFrom(t *testing.T, group controlplane.QueryGroup) any {
	t.Helper()
	encoded, err := json.Marshal(group.Plans[0].Plan.StrategyIR)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func compileEveryLevel(t *testing.T, group controlplane.QueryGroup) *strategy.CompiledPlan {
	t.Helper()
	result := evaluationCoreResult(t, group.Plans[0].Plan, group.QueryPlan.Normalization.DatasetContract)
	if terminals := result.LevelTerminals(); len(terminals) != 0 {
		t.Fatalf("levels refused: %+v", terminals)
	}
	return compileWithEvaluationCore(t, group.Plans[0].Plan, group.QueryPlan.Normalization.DatasetContract)
}
