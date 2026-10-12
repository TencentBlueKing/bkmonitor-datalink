// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"linkd/internal/jsonpath"
)

func assignmentForTest(t *testing.T, path string, value any) EnrichAssignment {
	t.Helper()
	target, err := jsonpath.ParseTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := NewEnrichAssignment(target, value)
	if err != nil {
		t.Fatal(err)
	}
	return assignment
}

func TestEvaluationAtomicRollbackAndFrozenPatchValues(t *testing.T) {
	input := map[string]any{"title": "original", "labels": map[string]any{}, "extra_data": map[string]any{"items": []any{nil, "old"}, "big": json.Number("9007199254740993")}}
	state, err := NewEnrichDocumentEvaluation(input, input)
	if err != nil {
		t.Fatal(err)
	}
	before := state.Measurement()
	value := map[string]any{"name": "first"}
	patch := assignmentForTest(t, "$.extra_data.object", value)
	value["name"] = "caller mutation"
	if err := state.Apply([]EnrichAssignment{patch}); err != nil {
		t.Fatal(err)
	}
	if err := state.Apply([]EnrichAssignment{assignmentForTest(t, "$.extra_data.object.name", "later")}); err != nil {
		t.Fatal(err)
	}
	wire, err := patch.Patch()
	if err != nil || string(wire.Value) != `{"name":"first"}` {
		t.Fatalf("record changed: %s %v", wire.Value, err)
	}
	patch.RuleID, patch.OperationID = "<rule>", "operation"
	wire, _ = patch.Patch()
	encoded, _ := json.Marshal(wire)
	if patch.EncodedSize() != len(encoded) {
		t.Fatalf("size %d != %d", patch.EncodedSize(), len(encoded))
	}
	state.Rollback(0)
	if !reflect.DeepEqual(state.Document(), input) || state.Measurement() != before {
		t.Fatal("rollback changed input or budget")
	}
	good := assignmentForTest(t, "$.title", "changed")
	bad := assignmentForTest(t, "$.extra_data.created.items[0]", true)
	if err := state.Apply([]EnrichAssignment{good, bad}); err == nil {
		t.Fatal("accepted nonexistent array")
	}
	if state.Document()["title"] != "original" || !reflect.DeepEqual(state.Document(), input) {
		t.Fatal("partial batch survived")
	}
	oversized := assignmentForTest(t, "$.content", strings.Repeat("x", (1<<20)-64))
	if err := state.Apply([]EnrichAssignment{oversized}); err == nil {
		t.Fatal("accepted oversized document")
	}
	if !reflect.DeepEqual(state.Document(), input) {
		t.Fatal("budget failure changed state")
	}
	if err := state.Freeze(); err != nil {
		t.Fatal(err)
	}
	if err := state.Apply([]EnrichAssignment{good}); err == nil {
		t.Fatal("modified frozen evaluation")
	}
}

func TestEvaluationViewCacheIsBoundToSavedPayload(t *testing.T) {
	event := Event{BKTenantID: "tenant", EventSourceID: "source", EventSourceVersion: 1, EventID: "event", EventEnrichment: EventEnrichment{EnrichConfigDigest: "release", Enrich: EventEnrichData{Evaluations: []EvaluationEnrich{{Severity: "warning", Data: JSONObject{"processors": json.RawMessage(`[]`)}}}}}}
	event = event.WithEnrichEvaluations(nil)
	calls := 0
	build := func() (*EnrichEvaluation, error) {
		calls++
		state, err := NewEnrichDocumentEvaluation(map[string]any{}, map[string]any{})
		if err == nil {
			err = state.Freeze()
		}
		return state, err
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if _, err := event.EnrichEvaluationView("warning", build); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if calls != 1 {
		t.Fatalf("rebuilt shared view %d times", calls)
	}
	changed := event.Clone()
	changed.Enrich.Evaluations[0].Data["processors"] = json.RawMessage(`[{}]`)
	if _, err := changed.EnrichEvaluationView("warning", build); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("reused stale payload")
	}
	changed = event.Clone()
	changed.BKTenantID = "other"
	if _, err := changed.EnrichEvaluationView("warning", build); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("reused another tenant's view")
	}
}
