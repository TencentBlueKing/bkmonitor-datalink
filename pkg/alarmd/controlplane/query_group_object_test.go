// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// objectStrategyDocument is one Threshold strategy in the legacy source shape.
// Every golden edit below starts from it and changes one thing.
func objectStrategyDocument(t *testing.T, edit func(document map[string]any)) json.RawMessage {
	t.Helper()
	const base = `{"id":1001,"bk_biz_id":2,"update_time":1725000000,"name":"cpu usage","labels":["ops"],
		"items":[{"id":11,"query_md5":"shared-query-md5","expression":"a","functions":[],
			"query_configs":[{"data_source_label":"bk_monitor","data_type_label":"time_series","agg_method":"avg","agg_interval":60,"agg_dimension":["host"],"agg_condition":[],"result_table_id":"system.cpu","metric_field":"usage","alias":"a","functions":[]}],
			"algorithms":[{"level":1,"type":"Threshold","unit_prefix":"","config":[[{"method":"gte","threshold":80}]]}],
			"unit":"percent"}],
		"detects":[{"level":1,"connector":"and","trigger_config":{"count":1,"check_window":1},"recovery_config":{"check_window":1}}]}`
	var document map[string]any
	if err := json.Unmarshal([]byte(base), &document); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(document)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func objectFirstItem(document map[string]any) map[string]any {
	return document["items"].([]any)[0].(map[string]any)
}

type objectBuild struct {
	group    controlplane.QueryGroup
	snapshot execution.SnapshotRevision
	object   execution.ObjectDigest
	context  execution.OutputContextDigest
}

// buildObjectCatalog compiles the documents into one catalog and returns its
// single Query Group with the three digests the golden cases compare.
func buildObjectCatalog(t *testing.T, protocol string, documents ...json.RawMessage) objectBuild {
	t.Helper()
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	strategies := make([]controlplane.SourceStrategy, 0, len(documents))
	for _, document := range documents {
		var header struct {
			ID json.Number `json:"id"`
		}
		if err := json.Unmarshal(document, &header); err != nil {
			t.Fatal(err)
		}
		strategies = append(strategies, controlplane.SourceStrategy{SourceID: header.ID.String(), Document: document, Identity: identity})
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: strategies, Planner: &recordingPlanner{facts: queryFacts(t)}, OutputProtocol: protocol,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 {
		t.Fatalf("expected one Query Group, got %d with dispositions %+v", len(catalog.QueryGroups), catalog.Dispositions)
	}
	group := catalog.QueryGroups[0]
	object, err := controlplane.DeriveQueryGroupObjectDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	context, err := controlplane.DeriveOutputContextDigest(group.Plans[0])
	if err != nil {
		t.Fatal(err)
	}
	return objectBuild{group: group, snapshot: catalog.SnapshotRevision, object: object, context: context}
}

// TestQueryGroupObjectDigestIgnoresEditsThatDoNotChangeExecution is the
// golden set for the digest scope. Each edit is one the snapshot revision
// counts as a change, which the case asserts first: a case whose edit the old
// digest also ignored would prove nothing about the new one. The object
// digest must then stay put and the output context digest must move.
func TestQueryGroupObjectDigestIgnoresEditsThatDoNotChangeExecution(t *testing.T) {
	cases := []struct {
		name           string
		before, after  func(map[string]any)
		protocolBefore string
		protocolAfter  string
	}{
		{
			name: "name, description, notice and update_time",
			after: func(document map[string]any) {
				document["name"] = "cpu usage renamed"
				document["description"] = "edited"
				document["notice"] = map[string]any{"user_groups": []any{float64(7)}}
				document["update_time"] = float64(1725000999)
			},
		},
		{
			name:   "labels only",
			before: func(document map[string]any) { document["labels"] = []any{"ops"} },
			after:  func(document map[string]any) { document["labels"] = []any{"ops", "tier-1"} },
		},
		{
			// A source with a Python strategy_revision may omit update_time;
			// the revision then falls back to a digest of the whole decoded
			// strategy, labels included.
			name: "labels only when update_time is zero and the revision falls back to a digest of the source",
			before: func(document map[string]any) {
				document["strategy_revision"] = float64(5)
				document["update_time"] = float64(0)
				document["labels"] = []any{"ops"}
			},
			after: func(document map[string]any) {
				document["strategy_revision"] = float64(5)
				document["update_time"] = float64(0)
				document["labels"] = []any{"ops", "tier-1"}
			},
		},
		{
			name:   "python strategy_revision only",
			before: func(document map[string]any) { document["strategy_revision"] = float64(5) },
			after:  func(document map[string]any) { document["strategy_revision"] = float64(6) },
		},
		{
			name:  "item id only",
			after: func(document map[string]any) { objectFirstItem(document)["id"] = float64(12) },
		},
		{
			name:           "wire format only, switched by the deployment",
			before:         func(document map[string]any) { document["strategy_revision"] = float64(5) },
			after:          func(document map[string]any) { document["strategy_revision"] = float64(5) },
			protocolBefore: "legacy",
			protocolAfter:  "native",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := buildObjectCatalog(t, test.protocolBefore, objectStrategyDocument(t, test.before))
			after := buildObjectCatalog(t, test.protocolAfter, objectStrategyDocument(t, test.after))
			if before.snapshot == after.snapshot {
				t.Fatalf("the edit is invisible to the snapshot revision, so this case does not exercise the scope: %s", before.snapshot)
			}
			if before.object != after.object {
				t.Fatalf("object digest moved on an edit that does not change execution: %s -> %s", before.object, after.object)
			}
			if before.context == after.context {
				t.Fatalf("output context digest did not move although the edit changed rendering context: %s", before.context)
			}
		})
	}
}

// TestQueryGroupObjectDigestMovesWhenExecutionChanges is the other half of
// the golden set: every edit here changes what a Worker evaluates, and the
// object digest must say so.
func TestQueryGroupObjectDigestMovesWhenExecutionChanges(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
	}{
		{name: "threshold", edit: func(document map[string]any) {
			objectFirstItem(document)["algorithms"] = []any{map[string]any{"level": float64(1), "type": "Threshold", "unit_prefix": "", "config": []any{[]any{map[string]any{"method": "gte", "threshold": float64(90)}}}}}
		}},
		{name: "algorithm kind", edit: func(document map[string]any) {
			objectFirstItem(document)["algorithms"] = []any{map[string]any{"level": float64(1), "type": "SimpleRingRatio", "unit_prefix": "", "config": map[string]any{"floor": float64(50), "ceil": nil}}}
		}},
		{name: "aggregation interval", edit: func(document map[string]any) {
			objectFirstItem(document)["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = float64(120)
		}},
		{name: "trigger window", edit: func(document map[string]any) {
			document["detects"].([]any)[0].(map[string]any)["trigger_config"] = map[string]any{"count": float64(2), "check_window": float64(3)}
		}},
		{name: "target", edit: func(document map[string]any) {
			objectFirstItem(document)["target"] = []any{[]any{map[string]any{"field": "bk_target_ip", "method": "eq", "value": []any{map[string]any{"bk_target_ip": "192.0.2.10"}}}}}
		}},
		{name: "unit", edit: func(document map[string]any) { objectFirstItem(document)["unit"] = "" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := buildObjectCatalog(t, "", objectStrategyDocument(t, nil))
			after := buildObjectCatalog(t, "", objectStrategyDocument(t, test.edit))
			if before.object == after.object {
				t.Fatalf("object digest did not move on an execution change: %s", before.object)
			}
		})
	}
	t.Run("plan membership", func(t *testing.T) {
		one := buildObjectCatalog(t, "", objectStrategyDocument(t, nil))
		two := buildObjectCatalog(t, "", objectStrategyDocument(t, nil), objectStrategyDocument(t, func(document map[string]any) {
			document["id"] = float64(1002)
			objectFirstItem(document)["id"] = float64(12)
		}))
		if one.object == two.object {
			t.Fatalf("object digest did not move when a Plan joined the Query Group: %s", one.object)
		}
	})
}

// TestQueryGroupObjectDigestIsIndependentFromSourceTraversalOrder mirrors the
// snapshot revision's order independence for the object digest.
func TestQueryGroupObjectDigestIsIndependentFromSourceTraversalOrder(t *testing.T) {
	first := objectStrategyDocument(t, nil)
	second := objectStrategyDocument(t, func(document map[string]any) {
		document["id"] = float64(1002)
		objectFirstItem(document)["id"] = float64(12)
	})
	forward := buildObjectCatalog(t, "", first, second)
	reverse := buildObjectCatalog(t, "", second, first)
	if forward.object != reverse.object {
		t.Fatalf("object digest changed with traversal order: %s != %s", forward.object, reverse.object)
	}
}

// TestQueryGroupObjectCarriesNoSourceDocument checks the projection by
// content rather than by digest: the marker planted in the source document
// must be absent from the execution object and present in the output context.
func TestQueryGroupObjectCarriesNoSourceDocument(t *testing.T) {
	const marker = "marker-that-only-the-source-document-holds"
	// A Python strategy_revision makes the compiler attach the output identity
	// and the subject facts; the forced legacy protocol makes it attach the
	// source document as well, so every column has something to be found in.
	built := buildObjectCatalog(t, "legacy", objectStrategyDocument(t, func(document map[string]any) {
		document["name"] = marker
		document["update_time"] = float64(1725009999)
		document["strategy_revision"] = float64(5)
	}))
	object, err := json.Marshal(controlplane.BuildQueryGroupObject(built.group))
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{marker, "1725009999", `"snapshot_revision"`, `"legacy_output"`, `"wire_format"`, `"subject_facts"`, `"source_compatibility"`, `"PlanRevision"`} {
		if strings.Contains(string(object), excluded) {
			t.Fatalf("execution object carries %q: %s", excluded, object)
		}
	}
	if !strings.Contains(string(object), `"output_identity"`) {
		t.Fatalf("execution object lacks the output identity the alert fingerprint is derived from: %s", object)
	}
	context, err := json.Marshal(controlplane.BuildOutputContext(built.group.Plans[0]))
	if err != nil {
		t.Fatal(err)
	}
	for _, included := range []string{marker, "1725009999", `"snapshot_revision":5`, `"wire_format"`, `"subject_facts"`, `"source_compatibility"`} {
		if !strings.Contains(string(context), included) {
			t.Fatalf("output context lacks %q: %s", included, context)
		}
	}
}

// TestPublishedPlanFieldsAreEachPlacedInOneDigest is the guard that keeps the
// scope honest after this change. Every field of the published types must be
// named below as execution content, as output context, or as a type whose
// own fields are classified in turn. A field added to any of these types
// fails this test until it is placed, so it cannot drift into the object
// digest, or out of it, unread.
func TestPublishedPlanFieldsAreEachPlacedInOneDigest(t *testing.T) {
	type placement struct {
		execution []string
		context   []string
		split     []string
		neither   []string
	}
	placements := map[reflect.Type]placement{
		reflect.TypeOf(controlplane.Catalog{}): {
			split:   []string{"QueryGroups"},
			neither: []string{"ObservationID", "SnapshotRevision", "Dispositions"},
		},
		reflect.TypeOf(controlplane.QueryGroup{}): {
			execution: []string{"Identity", "QueryPlan", "MembershipDigest", "ScheduleRevision"},
			split:     []string{"Plans"},
		},
		reflect.TypeOf(controlplane.FrozenPlan{}): {
			execution: []string{"Identity", "StateGeneration", "ScheduleSpec", "ScheduleRevision", "RequirementTemplates", "QueryPlans"},
			split:     []string{"Plan"},
			// PlanRevision digests the whole EvaluationPlanV2, update_time and
			// source document included, and nothing reads it.
			neither: []string{"PlanRevision"},
		},
		reflect.TypeOf(contract.EvaluationPlanV2{}): {
			execution: []string{"plan_id", "input_projection", "output_identity", "target_scope", "terminal_reason_code"},
			context:   []string{"source_compatibility", "subject_facts", "legacy_output", "wire_format"},
			split:     []string{"strategy_ref", "strategy_ir"},
		},
		reflect.TypeOf(contract.StrategyIRV2{}): {
			execution: []string{"schema", "required_features", "execution_semantics", "input_projection", "levels"},
			split:     []string{"strategy_ref"},
		},
		reflect.TypeOf(contract.StrategyRefV2{}): {
			execution: []string{"tenant_id", "strategy_id"},
			context:   []string{"revision", "snapshot_revision"},
		},
	}
	for typ, placed := range placements {
		t.Run(typ.String(), func(t *testing.T) {
			actual := jsonFieldNames(typ)
			var declared []string
			seen := map[string]string{}
			for column, names := range map[string][]string{"execution": placed.execution, "context": placed.context, "split": placed.split, "neither": placed.neither} {
				for _, name := range names {
					if previous, duplicate := seen[name]; duplicate {
						t.Fatalf("%s is placed in both %s and %s", name, previous, column)
					}
					seen[name] = column
					declared = append(declared, name)
				}
			}
			sort.Strings(declared)
			if !reflect.DeepEqual(actual, declared) {
				t.Fatalf("fields of %s are %v but the placement names %v; place every field in exactly one column", typ, actual, declared)
			}
		})
	}
}

func jsonFieldNames(typ reflect.Type) []string {
	names := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := field.Name
		if tag, ok := field.Tag.Lookup("json"); ok {
			if tagged := strings.Split(tag, ",")[0]; tagged != "" {
				name = tagged
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
