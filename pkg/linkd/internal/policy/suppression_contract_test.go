// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
)

func TestSuppressionTargetOnlyStillChecksMembership(t *testing.T) {
	spec := policySpecMap(t, Shield)
	delete(spec, "shield_type")
	spec["policy"] = map[string]any{}
	spec["scheme"] = []Scheme{{Type: "clip", Duration: 5, DurationType: "minute", Count: 3}}
	compiled, err := Compile(Suppression, encodeSpec(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	release := Release{Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: "p", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
	event := policyEvent(`[]`, domain.EnrichStatusSucceeded)
	event.Labels["model_id"] = domain.NewStringScalar("cmdb.host")
	event.Labels["model_inst_id"] = domain.NewStringScalar("123")
	view := mustEventView(t, event, nil)
	now := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	refs := []onemodel.InstanceRef{{ModelID: "cmdb.host", InstanceID: "123", EntityUID: "cmdb.host|123"}}
	for _, allowed := range []bool{false, true} {
		reader := policyTargets{}
		if allowed {
			reader.refs = refs
		}
		verdict, err := Evaluate(t.Context(), release, compiled, view, reader, now, false)
		if err != nil || verdict.Matched != allowed {
			t.Fatalf("allowed=%v verdict=%+v err=%v", allowed, verdict, err)
		}
	}
	selected, err := newCandidateIndex([]FrozenPolicy{{Release: release, Compiled: compiled}}).selectPolicies(t.Context(), nil)
	if err != nil || len(selected) != 1 {
		t.Fatalf("target-only candidate lost: %v %v", selected, err)
	}
}

func TestPolicyJSONPathReadOrderAndAvailability(t *testing.T) {
	mapping := map[string]FieldMapping{"owner": {Path: "$.extra_data.owner", ReadPath: `$['extra_data','labels']['owner']`, Kind: FieldText}}
	for _, value := range []any{"", false, json.Number("0"), nil, "custom"} {
		document := map[string]any{"extra_data": map[string]any{"owner": value}, "labels": map[string]any{"owner": "builtin"}}
		view, err := newFactView("tenant", "fingerprint", document, "warning", "triggered", time.Now(), mapping, nil, RelationContext{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := view.Field(t.Context(), "owner")
		if err != nil || !got.Present || !reflect.DeepEqual(got.Data, value) {
			t.Fatalf("got %+v %v want %#v", got, err, value)
		}
		delete(document["extra_data"].(map[string]any), "owner")
		got, err = view.Field(t.Context(), "owner")
		if err != nil || got.Data != "builtin" {
			t.Fatalf("configured second path not read: %+v %v", got, err)
		}
	}
	for _, path := range []string{"$.source_raw_data.secret", `$['labels','source_raw_data'].owner`} {
		if _, err := (FieldMapping{Path: "$.extra_data.owner", ReadPath: path}).ReadTargets(); err == nil {
			t.Fatal("raw payload read accepted")
		}
	}
}

func TestSuppressionRegexp2WithoutEngineSelection(t *testing.T) {
	for _, pattern := range []string{`(?<=GUID\()\w+(?=\))`, `(主机)-\1`, `\A主机-主机\z`, `\d+`} {
		spec := policySpecMap(t, Suppression)
		spec["policy"] = map[string]any{"expression": "A", "A": map[string]any{"target_key": "name", "condition": "regexp", "target_value": pattern}}
		c, err := Compile(Suppression, encodeSpec(t, spec))
		if err != nil {
			t.Fatal(pattern, err)
		}
		text := "GUID(主机) 主机-主机 １２"
		if pattern == `\A主机-主机\z` {
			text = "主机-主机"
		}
		result, err := c.Conditions[0].Match(t.Context(), Fields{Values: map[string]any{"name": text}})
		if err != nil || !result.Matched {
			t.Fatalf("%s: %+v %v", pattern, result, err)
		}
	}
}
