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
	"fmt"
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

func TestDynamicGroupResourceTenantKeyspacesAndRedaction(t *testing.T) {
	resources := *validResourcesConfig()
	resources.DynamicGroup = &DynamicGroupResource{Tenants: map[string]DynamicGroupTenantResource{
		"tenant-a": {Redis: RedisConfig{Address: "redis.example.com:6379", Password: "secret", Database: 1}, KeyPrefix: "bk_monitor:"},
		"tenant-b": {Redis: RedisConfig{Address: "redis.example.com:6379", Password: "other", Database: 2}, KeyPrefix: "bk_monitor:"},
	}}
	selected, err := (EnrichConfig{Processors: []EnrichProcessorConfig{{Type: "resource"}}}).SelectResources(resources)
	if err != nil || selected.DynamicGroup == nil {
		t.Fatalf("selected=%#v error=%v", selected, err)
	}
	selected.DynamicGroup.Tenants["tenant-a"] = DynamicGroupTenantResource{}
	if resources.DynamicGroup.Tenants["tenant-a"].KeyPrefix == "" {
		t.Fatal("tenant resource was shared")
	}
	redacted := resources.Redacted()
	if redacted.DynamicGroup.Tenants["tenant-a"].Redis.Password != redactedSecret {
		t.Fatal("dynamic group password exposed")
	}
	resource := resources.DynamicGroup.Tenants["tenant-b"]
	resource.Redis.Database = 1
	resources.DynamicGroup.Tenants["tenant-b"] = resource
	if err := resources.Validate(); err == nil || !strings.Contains(err.Error(), "share a keyspace") {
		t.Fatalf("shared tenant keyspace accepted: %v", err)
	}
}

func TestLoadDynamicGroupResource(t *testing.T) {
	path := writeConfig(t, `resources:
  dynamic_group:
    tenants:
      tenant-a:
        redis: {address: 'redis.example.com:6379', database: 2, password: private}
        key_prefix: 'bk_monitor:'
`)
	cfg, err := Load(path, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	resource := cfg.Resources.DynamicGroup.Tenants["tenant-a"]
	if resource.Redis.Database != 2 || resource.Redis.Password != "private" || resource.KeyPrefix != "bk_monitor:" {
		t.Fatalf("dynamic group resource=%#v", cfg.Resources.Redacted().DynamicGroup)
	}
}

func TestDynamicGroupResourceTenantLimit(t *testing.T) {
	tenants := make(map[string]DynamicGroupTenantResource, maxDynamicGroupTenants+1)
	for index := range maxDynamicGroupTenants + 1 {
		tenants[fmt.Sprintf("tenant-%d", index)] = DynamicGroupTenantResource{
			Redis: RedisConfig{Address: "redis.example.com:6379", Database: index}, KeyPrefix: "bk_monitor:",
		}
	}
	if err := (ResourcesConfig{DynamicGroup: &DynamicGroupResource{Tenants: tenants}}).Validate(); err == nil || !strings.Contains(err.Error(), "1 to 32") {
		t.Fatalf("tenant limit accepted: %v", err)
	}
}

func TestOneModelDorisConfigurationAndRedaction(t *testing.T) {
	good := ResourcesConfig{OneModel: &OneModelResource{Backend: "doris", Doris: &OneModelDorisResource{Address: "doris:9030", Database: "kingeye", Username: "reader", Password: "doris-private"}}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	redacted := good.Redacted()
	if redacted.OneModel.Doris.Password != redactedSecret || good.OneModel.Doris.Password != "doris-private" {
		t.Fatal("Doris redaction mutated source")
	}
	for _, change := range []func(*OneModelResource){func(r *OneModelResource) { r.Doris = nil }, func(r *OneModelResource) { r.Backend = "unknown" }, func(r *OneModelResource) { r.Backend = "elasticsearch" }, func(r *OneModelResource) { r.Doris.InstanceTable = "table; drop" }, func(r *OneModelResource) { r.Doris.Address = "host" }, func(r *OneModelResource) { r.Doris.Database = "" }, func(r *OneModelResource) { r.APIKey = "unused-secret" }} {
		invalid := good.Clone()
		change(invalid.OneModel)
		if invalid.Validate() == nil {
			t.Fatal("invalid Doris configuration accepted")
		}
	}
	hybrid := good.Clone()
	hybrid.OneModel.Addresses = []string{"http://topology:9200"}
	if err := hybrid.Validate(); err != nil {
		t.Fatal(err)
	}
}
