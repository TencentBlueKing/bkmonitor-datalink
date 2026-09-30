// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The values a strategy was written against, planted in every place the
// frozen object keeps one, so the projection can be checked for carrying
// none of them.
var (
	secretTargetKey     = "192.0.2.41|0"
	secretMember        = "member-inst-7731"
	secretGroup         = "dyn-group-9001"
	secretConditionHost = "host-0xdeadbeef"
	secretPromQL        = `sum(rate(cpu_usage{host="host-0xdeadbeef"}[5m]))`
	secretQueryString   = "host:host-0xdeadbeef AND level:error"
	secretThreshold     = `{"threshold":86.5,"method":"gte"}`
	secretMetricMerge   = "a * 100 / b"
	secretTriggerNote   = "SEED-trigger-note"
	secretRecoveryNote  = "SEED-recovery-note"
	secretPromQLMatch   = `{host="host-0xdeadbeef"}`
	secretModelValue    = "model-value-SEED"
	secretKeepColumn    = "keep-SEED"
	secretOffsetForward = "offset-SEED"
	secretArgument      = "arg-SEED"
)

// configObject is one Query Group object holding two Plans of strategy 4101
// (two items) and one of another strategy, with a value in every field the
// projection has to redact.
func configObject() controlplane.QueryGroupObject {
	clause := model.QueryClause{
		ReferenceName: "a", DataSource: "bk_monitor", Driver: "uq", TableID: "system.cpu_summary", FieldName: "usage",
		FieldSemantics: "gauge", TimeField: "time", Dimensions: []string{"bk_target_ip", "bk_target_cloud_id"},
		Functions:   []model.QueryFunction{{Method: "avg", Dimensions: []string{"bk_target_ip"}}, {Method: "rate", Window: "5m", Arguments: []model.QueryScalar{{Kind: model.QueryScalarNumber, NumberValue: "0.99"}, {Kind: model.QueryScalarString, StringValue: secretArgument}}}},
		KeepColumns: []string{secretKeepColumn}, OffsetForward: secretOffsetForward,
		SourceConditions: &model.QueryConditions{Fields: []model.QueryConditionField{{Field: "src", Operator: "eq", Values: []model.QueryScalar{{Kind: model.QueryScalarString, StringValue: secretConditionHost}}}}},
		TimeAggregation:  model.QueryFunction{Method: "avg_over_time", Window: "60s"},
		Conditions: model.QueryConditions{Fields: []model.QueryConditionField{
			{Field: "bk_target_ip", Operator: "eq", Values: []model.QueryScalar{{Kind: model.QueryScalarString, StringValue: "192.0.2.41"}, {Kind: model.QueryScalarString, StringValue: "192.0.2.42"}}},
			{Field: "hostname", Operator: "contains", Values: []model.QueryScalar{{Kind: model.QueryScalarString, StringValue: secretConditionHost}}},
		}, Connectors: []string{"and"}},
		QueryString: secretQueryString,
	}
	plan := func(strategy, business, planID string) controlplane.QueryGroupPlanObject {
		return controlplane.QueryGroupPlanObject{
			Identity:        model.PlanIdentity{TenantID: "default", BusinessID: business, StrategyID: strategy},
			StateGeneration: "gen-1", PlanID: planID,
			ScheduleSpec: model.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 15, Timezone: "Asia/Shanghai", CompletionDeadlineOffsetSeconds: 30},
			RequirementTemplates: []model.DataRequirementTemplate{{
				DatasetName: "primary", Role: "current", ConsumerLevelID: 1, LogicalQueryRef: "a",
				RelativeWindow: model.RelativeQueryWindow{StartOffsetSeconds: -300, EndOffsetSeconds: 0}, StepMillis: 60000,
				ReadinessClass: "settled", PointOffsetsSeconds: []int64{-240, -180, -120, -60, 0},
			}},
			QueryPlans: map[model.LogicalQueryRef]model.QueryPlanFacts{"a": {QueryList: []model.QueryClause{clause}}},
			TargetPlan: &contract.TargetPlanV1{SchemaVersion: 1, Rule: contract.TargetPlanRuleHostID,
				Identity:      contract.TargetPlanIdentityV1{Dimensions: []string{"bk_target_ip", "bk_target_cloud_id"}, ModelDimension: "bk_obj_id", ModelValue: secretModelValue},
				StaticKeys:    []string{secretTargetKey, "192.0.2.42|0", "192.0.2.43|0"},
				StaticMembers: []contract.TargetPlanMemberV1{{ModelID: "host", ModelInstID: secretMember}},
				DynamicGroups: []string{secretGroup}, DynamicTopologies: []contract.TargetPlanTopologyV1{{BusinessID: business, ObjectID: "set", InstanceID: "12"}}},
			NoData: &contract.NoDataConfigV1{Continuous: 5, Level: 2, AggDimension: []string{"bk_target_ip"}, TrackingHorizonSeconds: 3600},
			StrategyIR: contract.StrategyIRV2{Levels: []contract.LevelIRV2{{
				Definition: contract.LevelDefinitionV2{LevelID: 1, LevelCode: "fatal", Priority: 3}, Connector: "and",
				DetectPlan:   contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Version: 1, Config: json.RawMessage(secretThreshold)}}},
				TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":5,"required_anomalies":3,"step_seconds":60,"note":"` + secretTriggerNote + `"}`)},
				RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":5,"note":"` + secretRecoveryNote + `"}`)},
			}}},
		}
	}
	return controlplane.QueryGroupObject{
		Identity: "qg-4101-a",
		QueryPlan: model.QueryPlanFacts{Provider: "uq", TenantID: "default", BusinessID: "2", SpaceScope: "bkcc__2",
			SourceSemantics: []string{"time_series"}, QueryDelaySeconds: 10, StepMillis: 60000, MetricMerge: secretMetricMerge,
			PromQL: &model.PromQLQuery{Expression: secretPromQL, Match: secretPromQLMatch}},
		Plans: []controlplane.QueryGroupPlanObject{plan("4101", "2", "plan-4101-item-1"), plan("4101", "2", "plan-4101-item-2"), plan("4199", "2", "plan-4199")},
	}
}

// The projection carries what a strategy is configured with -- table,
// metric, dimensions, functions, schedule, target shape, levels, algorithms,
// windows -- and none of the values it was written against. Checked on the
// wire, where a reader would see them.
func TestTheConfigProjectionCarriesNamesAndCountsAndNoValue(t *testing.T) {
	configs := StrategyPlanConfigsOf(configObject(), "4101", "2")
	if configs.Refusal != "" || !configs.Redacted || len(configs.Items) != 2 {
		t.Fatalf("configs = %+v, want the strategy's two items, redacted, no refusal", configs)
	}
	wire, err := json.Marshal(configs)
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
	for _, secret := range []string{secretTargetKey, "192.0.2.42", secretMember, secretGroup, secretConditionHost, secretPromQL, secretQueryString, "86.5", "0.99", `"12"`,
		secretMetricMerge, secretTriggerNote, secretRecoveryNote, secretPromQLMatch, secretModelValue, secretKeepColumn, secretOffsetForward, secretArgument, "SEED"} {
		if strings.Contains(text, secret) {
			t.Fatalf("the wire carries a value the strategy was written against (%q):\n%s", secret, text)
		}
	}
	item := configs.Items[0]
	if item.PlanID != "plan-4101-item-1" || item.Schedule.IntervalSeconds != 60 || item.Schedule.AlignmentSeconds != 15 || item.Schedule.CompletionOffsetSeconds != 30 {
		t.Fatalf("schedule = %+v", item.Schedule)
	}
	if item.Query.Provider != "uq" || item.Query.Business != "2" || item.Query.PromQL == nil || !item.Query.PromQL.Present || item.Query.PromQL.ExpressionBytes != len(secretPromQL) {
		t.Fatalf("query = %+v, want the provider, the business and the expression's size only", item.Query)
	}
	if item.Query.MetricMerge == nil || !item.Query.MetricMerge.Present || item.Query.MetricMerge.Bytes != len(secretMetricMerge) {
		t.Fatalf("metric merge = %+v, want present and its size only: it is an expression the author wrote", item.Query.MetricMerge)
	}
	if len(item.Query.Clauses) != 1 {
		t.Fatalf("clauses = %+v, want the one clause of logical query a", item.Query.Clauses)
	}
	clause := item.Query.Clauses[0]
	if clause.Reference != "a" || clause.TableID != "system.cpu_summary" || clause.Field != "usage" || len(clause.Dimensions) != 2 ||
		len(clause.Functions) != 2 || clause.Functions[1].Window != "5m" || clause.Functions[1].Arguments != 2 ||
		clause.TimeAggregation == nil || clause.TimeAggregation.Method != "avg_over_time" {
		t.Fatalf("clause = %+v", clause)
	}
	if len(clause.Conditions) != 2 || clause.Conditions[0].Field != "bk_target_ip" || clause.Conditions[0].Operator != "eq" || clause.Conditions[0].Values != 2 || clause.Conditions[1].Values != 1 {
		t.Fatalf("conditions = %+v, want fields, operators and value counts", clause.Conditions)
	}
	if clause.QueryString == nil || clause.QueryString.Bytes != len(secretQueryString) {
		t.Fatalf("query string = %+v, want its size only", clause.QueryString)
	}
	if item.Target.Kind != TargetKindPlan || item.Target.Plan == nil || item.Target.Plan.Rule != "host_id" ||
		item.Target.Plan.StaticKeys != 3 || item.Target.Plan.StaticMembers != 1 || item.Target.Plan.DynamicGroups != 1 || item.Target.Plan.DynamicTopologies != 1 ||
		len(item.Target.Plan.IdentityDimensions) != 2 {
		t.Fatalf("target = %+v, want the plan's rule, identity dimensions and member counts", item.Target)
	}
	if item.NoData == nil || item.NoData.Continuous != 5 || item.NoData.Level != 2 || len(item.NoData.AggDimension) != 1 ||
		item.NoData.TrackingHorizonSeconds != 3600 {
		t.Fatalf("no_data = %+v, want the frozen tracking horizon beside the window and level", item.NoData)
	}
	if len(item.Levels) != 1 || item.Levels[0].LevelID != 1 || len(item.Levels[0].Algorithms) != 1 ||
		item.Levels[0].Algorithms[0].Type != "Threshold" || item.Levels[0].Algorithms[0].ConfigBytes != len(secretThreshold) {
		t.Fatalf("levels = %+v, want the algorithm by type and size", item.Levels)
	}
	trigger, recovery := item.Levels[0].Trigger, item.Levels[0].Recovery
	if trigger.Type != "N_OF_M" || trigger.WindowSize == nil || *trigger.WindowSize != 5 || trigger.RequiredAnomalies == nil || *trigger.RequiredAnomalies != 3 ||
		trigger.StepSeconds == nil || *trigger.StepSeconds != 60 || trigger.UnknownKeys != 1 {
		t.Fatalf("trigger = %+v, want the three window counts by key and one unknown key counted, not carried", trigger)
	}
	if recovery.Type != "CONTINUOUS_TRIGGER_MISS" || recovery.Enabled == nil || !*recovery.Enabled || recovery.ConsecutiveWindows == nil || *recovery.ConsecutiveWindows != 5 || recovery.UnknownKeys != 1 {
		t.Fatalf("recovery = %+v, want enabled and the window by key and one unknown key counted", recovery)
	}
	// A known key holding a value that is not a count is unknown too, and a
	// document that is not an object is said to be undecodable, not carried.
	odd := typedPlanConfigOf(contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":"` + secretTriggerNote + `","required_anomalies":-1,"step_seconds":60.5}`)})
	if odd.WindowSize != nil || odd.RequiredAnomalies != nil || odd.StepSeconds != nil || odd.UnknownKeys != 3 {
		t.Fatalf("odd trigger = %+v, want no count read and three unknown", odd)
	}
	if text := typedPlanConfigOf(contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`"` + secretTriggerNote + `"`)}); !text.Undecodable {
		t.Fatalf("a document that is not an object = %+v, want undecodable", text)
	}
	if len(item.Requirements) != 1 || item.Requirements[0].WindowStart != -300 || item.Requirements[0].Points != 5 || item.Requirements[0].ConsumerLevelID != 1 {
		t.Fatalf("requirements = %+v", item.Requirements)
	}
	// A frozen scope instead of a plan: groups and conditions as shapes.
	scoped := configObject()
	scoped.Plans[0].TargetPlan = nil
	scoped.Plans[0].TargetScope = &contract.TargetScopeV2{Groups: []contract.TargetScopeGroupV2{
		{Conditions: []contract.TargetScopeConditionV2{{Field: "host", Method: "in", Keys: []string{secretTargetKey, "192.0.2.42|0"}}}},
		{Conditions: []contract.TargetScopeConditionV2{{Field: "topo", Method: "in", Keys: []string{"set|12"}}}},
	}}
	scopedConfigs := StrategyPlanConfigsOf(scoped, "4101", "2")
	target := scopedConfigs.Items[0].Target
	if target.Kind != TargetKindScope || target.Scope == nil || target.Scope.Groups != 2 || len(target.Scope.Conditions) != 2 ||
		target.Scope.Conditions[0].Keys != 2 || target.Scope.Conditions[1].Group != 1 {
		t.Fatalf("scope target = %+v", target)
	}
	if scopedWire, _ := json.Marshal(scopedConfigs); strings.Contains(string(scopedWire), secretTargetKey) || strings.Contains(string(scopedWire), "set|12") {
		t.Fatalf("scope keys reached the wire: %s", scopedWire)
	}
	// No target at all, and a strategy the object has no Plan of.
	bare := configObject()
	bare.Plans[0].TargetPlan = nil
	if kind := StrategyPlanConfigsOf(bare, "4101", "2").Items[0].Target.Kind; kind != TargetKindNone {
		t.Fatalf("target kind without scope or plan = %q, want %s", kind, TargetKindNone)
	}
	if missing := StrategyPlanConfigsOf(configObject(), "4102", ""); missing.Refusal != ConfigPlanNotInObject || len(missing.Items) != 0 {
		t.Fatalf("a strategy the object has no Plan of = %+v, want %s", missing, ConfigPlanNotInObject)
	}
	if narrowed := StrategyPlanConfigsOf(configObject(), "4101", "9"); narrowed.Refusal != ConfigPlanNotInObject {
		t.Fatalf("narrowed to a business the strategy has no Plan in = %+v, want %s", narrowed, ConfigPlanNotInObject)
	}
}

// configHandler is the standing route with a loader the test controls: it
// counts its calls, answers by digest, and can fail.
type configLoader struct {
	calls   int
	objects map[string]controlplane.QueryGroupObject
	err     error
}

func (l *configLoader) load(_ context.Context, digest string) (controlplane.QueryGroupObject, error) {
	l.calls++
	if l.err != nil {
		return controlplane.QueryGroupObject{}, l.err
	}
	object, ok := l.objects[digest]
	if !ok {
		return controlplane.QueryGroupObject{}, errors.New("catalog object missing")
	}
	return object, nil
}

func configHandler(t *testing.T, facts map[string]StrategyLookupFacts, loader StrategyObjectLoader) http.Handler {
	t.Helper()
	snapshots := healthySnapshots()
	snapshots[0].Owned, snapshots[0].Determined = 1, 1
	snapshots[0].OwnedObjects = []string{"qg-4101-a"}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 1, Known: true, IDs: []string{"qg-4101-a"}}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(id string) StrategyLookupFacts { return facts[id] }
	return WithStrategyStanding(handler, service, lookup, nil, loader, nil, "pod-a", func() time.Time { return now }, 0)
}

// include=config reads each Plan's object once and attaches the projection;
// without it nothing is read and nothing is attached; a word outside the
// include vocabulary is refused rather than ignored.
func TestIncludeConfigReadsOncePerPlanAndOnlyWhenAsked(t *testing.T) {
	loader := &configLoader{objects: map[string]controlplane.QueryGroupObject{"d-a": configObject()}}
	facts := map[string]StrategyLookupFacts{"4101": {Available: true, Found: true, Plans: plans(planA)}}
	handler := configHandler(t, facts, loader.load)

	status, body := get(t, handler, "/api/strategies/4101")
	if status != http.StatusOK || loader.calls != 0 {
		t.Fatalf("without include: status %d, loader calls %d, want 200 and no read", status, loader.calls)
	}
	if plan := body["plans"].([]any)[0].(map[string]any); plan["config"] != nil {
		t.Fatalf("without include the plan carries config: %v", plan["config"])
	}

	status, body = get(t, handler, "/api/strategies/4101?include=config")
	if status != http.StatusOK || loader.calls != 1 {
		t.Fatalf("with include: status %d, loader calls %d, want 200 and one read", status, loader.calls)
	}
	config, _ := body["plans"].([]any)[0].(map[string]any)["config"].(map[string]any)
	if config == nil || config["redacted"] != true || config["refusal"] != nil {
		t.Fatalf("config = %v, want redacted content and no refusal", config)
	}
	items, _ := config["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v, want the strategy's two items", config["items"])
	}
	first := items[0].(map[string]any)
	if first["plan_id"] != "plan-4101-item-1" || first["target"].(map[string]any)["kind"] != TargetKindPlan {
		t.Fatalf("first item = %v", first)
	}
	if wire, _ := json.Marshal(body); strings.Contains(string(wire), secretTargetKey) || strings.Contains(string(wire), secretPromQL) {
		t.Fatalf("the answer carries a strategy value: %s", wire)
	}

	status, body = get(t, handler, "/api/strategies/4101?include=everything")
	if status != http.StatusBadRequest || body["error"] != "INCLUDE_UNKNOWN" || body["include"] != "everything" {
		t.Fatalf("unknown include word: status %d body %v, want 400 INCLUDE_UNKNOWN naming it", status, body)
	}
	if loader.calls != 1 {
		t.Fatalf("a refused request read the store: %d calls", loader.calls)
	}
}

// What a Plan carries when its configuration cannot be read: the reason, in
// place of content, so a reader tells "not configured" from "not read". The
// loader is asked at most MaxStrategyConfigReads times per request.
func TestIncludeConfigNamesWhyAPlanHasNoConfig(t *testing.T) {
	t.Run("loader not wired", func(t *testing.T) {
		handler := configHandler(t, map[string]StrategyLookupFacts{"4101": {Available: true, Found: true, Plans: plans(planA)}}, nil)
		_, body := get(t, handler, "/api/strategies/4101?include=config")
		config := body["plans"].([]any)[0].(map[string]any)["config"].(map[string]any)
		if config["refusal"] != ConfigLoaderNotWired || config["items"] != nil {
			t.Fatalf("config = %v, want %s", config, ConfigLoaderNotWired)
		}
	})
	t.Run("the store did not answer, the object is not there, the bytes are not the object", func(t *testing.T) {
		for _, test := range []struct {
			err  error
			want string
		}{
			{errors.New("redis: connection refused"), ConfigObjectUnreadable},
			{fmt.Errorf("load: %w", controlplane.ErrCatalogObjectUnavailable), ConfigObjectMissing},
			{fmt.Errorf("%w: not a Query Group object of this contract", controlplane.ErrCatalogObjectCorrupt), ConfigObjectCorrupt},
		} {
			loader := &configLoader{err: test.err}
			handler := configHandler(t, map[string]StrategyLookupFacts{"4101": {Available: true, Found: true, Plans: plans(planA)}}, loader.load)
			_, body := get(t, handler, "/api/strategies/4101?include=config")
			config := body["plans"].([]any)[0].(map[string]any)["config"].(map[string]any)
			if config["refusal"] != test.want {
				t.Fatalf("for %v config = %v, want %s", test.err, config, test.want)
			}
			if wire, _ := json.Marshal(body); strings.Contains(string(wire), "connection refused") || strings.Contains(string(wire), "not a Query Group") {
				t.Fatalf("the store's error text reached the wire: %s", wire)
			}
		}
	})
	t.Run("plan without a digest, and an object without the strategy's Plan", func(t *testing.T) {
		loader := &configLoader{objects: map[string]controlplane.QueryGroupObject{"d-a": configObject()}}
		other := planA
		other.QueryGroup, other.ObjectDigest = "qg-4101-t", ""
		handler := configHandler(t, map[string]StrategyLookupFacts{"4177": {Available: true, Found: true, Plans: plans(planA, other)}}, loader.load)
		_, body := get(t, handler, "/api/strategies/4177?include=config")
		plansOut := body["plans"].([]any)
		if config := plansOut[0].(map[string]any)["config"].(map[string]any); config["refusal"] != ConfigPlanNotInObject {
			t.Fatalf("an object without this strategy's Plan = %v, want %s", config, ConfigPlanNotInObject)
		}
		if config := plansOut[1].(map[string]any)["config"].(map[string]any); config["refusal"] != ConfigNoObjectDigest {
			t.Fatalf("a Plan without a digest = %v, want %s", config, ConfigNoObjectDigest)
		}
		if loader.calls != 1 {
			t.Fatalf("loader calls = %d, want one: a Plan without a digest is not read", loader.calls)
		}
	})
	t.Run("read bound", func(t *testing.T) {
		loader := &configLoader{objects: map[string]controlplane.QueryGroupObject{}}
		var refs []StrategyPlanRef
		for index := 0; index < MaxStrategyConfigReads+3; index++ {
			ref := planA
			ref.QueryGroup, ref.ObjectDigest = fmt.Sprintf("qg-4101-%d", index), fmt.Sprintf("d-%d", index)
			loader.objects[ref.ObjectDigest] = configObject()
			refs = append(refs, ref)
		}
		handler := configHandler(t, map[string]StrategyLookupFacts{"4101": {Available: true, Found: true, Plans: refs}}, loader.load)
		_, body := get(t, handler, "/api/strategies/4101?include=config")
		plansOut := body["plans"].([]any)
		if loader.calls != MaxStrategyConfigReads {
			t.Fatalf("loader calls = %d, want the bound %d", loader.calls, MaxStrategyConfigReads)
		}
		if config := plansOut[MaxStrategyConfigReads-1].(map[string]any)["config"].(map[string]any); config["refusal"] != nil {
			t.Fatalf("the last Plan within the bound = %v, want content", config)
		}
		for _, past := range plansOut[MaxStrategyConfigReads:] {
			if config := past.(map[string]any)["config"].(map[string]any); config["refusal"] != ConfigReadBoundExceeded {
				t.Fatalf("a Plan past the bound = %v, want %s", config, ConfigReadBoundExceeded)
			}
		}
	})
}
