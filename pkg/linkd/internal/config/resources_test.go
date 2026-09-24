// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResourcesRequiredOnlyBySelectedProcessors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		enrich    EnrichConfig
		resources ResourcesConfig
		want      string
	}{
		{name: "empty chain"},
		{name: "unused incomplete resources", resources: ResourcesConfig{MySQL: &MySQLResource{}, OneModel: &OneModelResource{}}},
		{name: "missing mysql", enrich: EnrichConfig{Processors: []EnrichProcessorConfig{{Type: "source"}}}, want: "resources.mysql"},
		{name: "invalid selected mysql", enrich: EnrichConfig{Processors: []EnrichProcessorConfig{{Type: "source"}}}, resources: ResourcesConfig{MySQL: &MySQLResource{}}, want: "resources.mysql"},
		{name: "onemodel missing", enrich: EnrichConfig{Processors: []EnrichProcessorConfig{{Type: "resource"}}}, resources: ResourcesConfig{MySQL: validResourcesConfig().MySQL}, want: "resources.onemodel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.enrich.SelectResources(tc.resources)
			if (tc.want == "" && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
				t.Fatalf("error=%v want=%s", err, tc.want)
			}
		})
	}
}

func TestSourceImportsDoNotNeedResourcesAndOldInputIsRejected(t *testing.T) {
	source := validEventSource()
	source.Enrich.Processors = []EnrichProcessorConfig{{Type: "source"}}
	if err := ValidateEventSources([]EventSource{source}, SeverityConfig{}); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, "event_sources:\n  - event_source_id: source-a\n    enrich:\n      datasources: {}\n")
	if _, err := Load(path, Overrides{}); err == nil || !strings.Contains(err.Error(), "datasources") {
		t.Fatalf("old input accepted: %v", err)
	}
	// 已持久化历史 Release 只读解析；旧连接不进入新类型，也不会被重新发布或返回。
	var stored EventSource
	if err := json.Unmarshal([]byte(`{"event_source_id":"source-a","enrich":{"processors":[],"datasources":{"mysql":{"password":"historical-secret"}}}}`), &stored); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "historical-secret") || strings.Contains(string(encoded), "datasources") {
		t.Fatal("historical resource survived typed boundary")
	}
}
