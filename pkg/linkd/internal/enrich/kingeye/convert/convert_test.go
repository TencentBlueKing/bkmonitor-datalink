// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package convert

import (
	"encoding/json"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich/custom"
	"linkd/internal/onemodel"
)

func TestKingeyeNormalFixtureKeepsGroupingAndOrder(t *testing.T) {
	input := Input{NormalRules: []json.RawMessage{
		json.RawMessage(`{"name":"same","match_rules":{"A":{"field":"content","value":"alarm","condition":"wildcard"},"expression":"A"},"enrich_settings":[{"type":"replace","fields":[{"key":"content"}],"rules":[{"field":"alarm1","value":"alarm22","is_regex":false}]},{"type":"replace","fields":[{"key":"content"}],"rules":[{"field":"alarm221","value":"success","is_regex":false}]}]}`),
		json.RawMessage(`{"name":"same","match_rules":{"A":{"field":"content","value":"success","condition":"wildcard"},"expression":"A"},"enrich_settings":[{"type":"field_adjust","fields":[{"key":"name","value":"${content}-1"}]}]}`)}}
	result, err := Convert(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Enrich.Processors) != 1 || len(result.Report) != 2 || result.Report[0].Status != "converted" {
		t.Fatalf("%+v", result)
	}
	p, err := custom.Compile("fields", result.Enrich.Processors[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	alert := map[string]any{"title": "raw", "content": "alarm11"}
	run, err := p.Execute(t.Context(), alert, alert, "tenant", custom.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := domain.ApplyEnrichPatches(alert, run.Patches)
	if err != nil || effective["title"] != "success-1" || effective["content"] != "success" {
		t.Fatalf("effective=%v err=%v", effective, err)
	}
}

func TestCaptureNumberingAndUnsupportedAreReported(t *testing.T) {
	for _, tc := range []struct{ pattern, source, want string }{{`[0-9]+`, `x12 y34`, `12/34`}, {`([0-9]+)`, `x12 y34`, `12/34`}, {`([a-z]+)=([0-9]+)`, `a=12 b=34`, `a/12`}} {
		raw, _ := json.Marshal(map[string]any{"enrich_settings": []any{map[string]any{"type": "extract", "rules": []any{map[string]any{"field": "content", "value": tc.pattern}}, "fields": []any{map[string]any{"key": "name", "value": "$1/$2"}}}}})
		r, err := Convert(Input{NormalRules: []json.RawMessage{raw}})
		if err != nil || len(r.Enrich.Processors) != 1 {
			t.Fatalf("%+v %v", r, err)
		}
		p, err := custom.Compile("fields", r.Enrich.Processors[0].Config)
		if err != nil {
			t.Fatal(err)
		}
		alert := map[string]any{"content": tc.source}
		run, err := p.Execute(t.Context(), alert, alert, "tenant", custom.Sources{})
		if err != nil {
			t.Fatal(err)
		}
		effective, err := domain.ApplyEnrichPatches(alert, run.Patches)
		if err != nil || effective["title"] != tc.want {
			t.Fatalf("%s: %v %v", tc.pattern, effective, err)
		}
	}
	r, err := Convert(Input{NormalRules: []json.RawMessage{json.RawMessage(`{"enrich_settings":[{"type":"extract","rules":[{"field":"content","value":"(?<=x)y"}],"fields":[{"key":"name","value":"$1"}]}]}`)}})
	if err != nil || len(r.Enrich.Processors) != 0 || r.Report[0].Status != "unsupported" || r.Report[0].Path != "$.normal_rules[0]" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestCMDBRequiresMetadataAndReportsReview(t *testing.T) {
	raw := json.RawMessage(`{"alarm_object_id":1,"inst_rules":{"expression":"A","A":{"field":"ip","value":"bk_host_innerip","condition":"term"}},"enrich_fields":[{"key":"owner","value":"operator"}]}`)
	input := Input{CMDBRules: []json.RawMessage{raw}, FieldMappings: map[string]string{"ip": "$.labels.ip", "owner": "$.labels.owner"}}
	r, err := Convert(input)
	if err != nil || r.Report[0].Status != "unsupported" {
		t.Fatalf("%+v %v", r, err)
	}
	input.Models = map[string]Model{"1": {ModelID: "cw-Host", Attributes: map[string]onemodel.InstanceAttributeType{"bk_host_innerip": onemodel.InstanceAttributeKeyword}}}
	r, err = Convert(input)
	if err != nil || r.Report[0].Status != "needs_review" || len(r.Enrich.Processors) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}
