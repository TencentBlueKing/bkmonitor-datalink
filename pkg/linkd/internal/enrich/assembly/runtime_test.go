// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package assembly

import (
	"testing"
	"time"

	"linkd/internal/config"
)

func TestRuntimeOpensOnlyRequestedResources(t *testing.T) {
	source := config.EventSource{EventSourceID: "test"}
	resources := config.ResourcesConfig{MySQL: &config.MySQLResource{Address: "unreachable:1"}, KingeyeDisplay: &config.DisplayResource{}, OneModel: &config.OneModelResource{Addresses: []string{"http://127.0.0.1:1"}}}
	runtime, err := Open(t.Context(), source, resources, 4, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.dataSources != nil || runtime.display != nil || runtime.transport != nil {
		t.Fatal("unused dependencies opened")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	source.Enrich.Processors = []config.EnrichProcessorConfig{{Type: "cmdb", Config: map[string]any{"rules": []any{map[string]any{"id": "host", "lookup": map[string]any{"model_id": "host", "where": map[string]any{"field": "model_inst_id", "type": "keyword", "operator": "eq", "value": map[string]any{"literal": "1"}}}, "assignments": []any{map[string]any{"target": "$.labels.name", "value": map[string]any{"literal": "ok"}}}}}}}}
	runtime, err = Open(t.Context(), source, resources, 4, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	if runtime.dataSources != nil || runtime.display != nil || runtime.transport == nil || runtime.sources.CMDB == nil {
		t.Fatal("CMDB initialized unrelated resources")
	}
}
