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
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"linkd/internal/kafkaclient"
)

func TestSeverityConfig(t *testing.T) {
	t.Parallel()
	defaults := (SeverityConfig{}).WithDefaults()
	if priority, ok := defaults.Priority("critical"); !ok || priority != 1 {
		t.Fatalf("critical priority = %d, %v", priority, ok)
	}
	customDefault := (SeverityConfig{DefaultSeverity: "critical"}).WithDefaults()
	if customDefault.DefaultSeverity != "critical" || len(customDefault.Levels) != 3 {
		t.Fatalf("default table with custom fallback = %#v", customDefault)
	}
	for _, test := range []SeverityConfig{
		{DefaultSeverity: "missing", Levels: []SeverityLevel{{Name: "warning", Priority: 2}}},
		{DefaultSeverity: "warning", Levels: []SeverityLevel{{Name: "warning", Priority: 2}, {Name: "warning", Priority: 3}}},
		{DefaultSeverity: "warning", Levels: []SeverityLevel{{Name: "warning", Priority: 2}, {Name: "info", Priority: 2}}},
	} {
		if err := test.Validate(); err == nil {
			t.Fatalf("Validate(%#v) unexpectedly succeeded", test)
		}
	}
}

func TestEnrichProcessorConfigJSONYAMLRoundTrip(t *testing.T) {
	for _, codec := range []struct {
		name      string
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{{"json", json.Marshal, json.Unmarshal}, {"yaml", yaml.Marshal, yaml.Unmarshal}} {
		t.Run(codec.name, func(t *testing.T) {
			source := validEventSource()
			source.Enrich.Processors = []EnrichProcessorConfig{{
				Type: "strategy", Config: map[string]any{"web_saas_module_url": "https://example.com/kingeye/"},
			}}
			data, err := codec.marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			var restored EventSource
			if err := codec.unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if got := restored.Enrich.Processors[0].Config["web_saas_module_url"]; got != "https://example.com/kingeye/" {
				t.Fatalf("processor config=%#v", restored.Enrich.Processors[0].Config)
			}
		})
	}
}

func TestEnrichProcessorConfigCloneDoesNotShareConfig(t *testing.T) {
	source := validEventSource()
	source.Enrich.Processors = []EnrichProcessorConfig{{
		Type: "strategy",
		Config: map[string]any{
			"web_saas_module_url": "https://example.com/kingeye/",
			"nested":              map[string]any{"enabled": true},
		},
	}}
	cloned := source.WithDefaults()
	cloned.Enrich.Processors[0].Config["web_saas_module_url"] = "https://changed.example.com/"
	cloned.Enrich.Processors[0].Config["nested"].(map[string]any)["enabled"] = false
	if source.Enrich.Processors[0].Config["web_saas_module_url"] != "https://example.com/kingeye/" ||
		source.Enrich.Processors[0].Config["nested"].(map[string]any)["enabled"] != true {
		t.Fatalf("processor config clone shares state: %#v", source.Enrich.Processors[0].Config)
	}
}

func TestEventSourceDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	source := validEventSource().WithDefaults()
	if source.Cleaner.Type != CleanerTypeStandard || source.FingerprintMode != FingerprintModeField || source.FingerprintField != "source_alert_id" {
		t.Fatalf("defaults = %#v", source)
	}
	if err := ValidateEventSources([]EventSource{source}, SeverityConfig{}); err != nil {
		t.Fatalf("ValidateEventSources() error = %v", err)
	}
	explicitFieldMode := validEventSource()
	explicitFieldMode.FingerprintMode = FingerprintModeField
	if got := explicitFieldMode.WithDefaults().FingerprintField; got != "source_alert_id" {
		t.Fatalf("explicit field mode default = %q", got)
	}
	duplicate := []EventSource{source, source}
	if err := ValidateEventSources(duplicate, SeverityConfig{}); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate error = %v", err)
	}
	legacy := validEventSource()
	legacy.Cleaner.Type = "event_v1"
	if err := ValidateEventSources([]EventSource{legacy}, SeverityConfig{}); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("legacy cleaner type error = %v", err)
	}
	source.Enrich.Processors = []EnrichProcessorConfig{{Type: "strategy"}, {Type: "display"}}
	if err := ValidateEventSources([]EventSource{source}, SeverityConfig{}); err != nil {
		t.Fatalf("ValidateEventSources() enrich error = %v", err)
	}
	duplicateProcessor := source
	duplicateProcessor.Enrich.Processors = []EnrichProcessorConfig{{Type: "strategy"}, {Type: "strategy"}}
	if err := ValidateEventSources([]EventSource{duplicateProcessor}, SeverityConfig{}); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate enrich processor error = %v", err)
	}
	emptyProcessor := source
	emptyProcessor.Enrich.Processors = []EnrichProcessorConfig{{}}
	if err := ValidateEventSources([]EventSource{emptyProcessor}, SeverityConfig{}); err == nil || !strings.Contains(err.Error(), "type is required") {
		t.Fatalf("empty enrich processor error = %v", err)
	}
	unsupportedProcessorConfig := source
	unsupportedProcessorConfig.Enrich.Processors = []EnrichProcessorConfig{{Type: "display", Config: map[string]any{"key": "value"}}}
	if err := ValidateEventSources([]EventSource{unsupportedProcessorConfig}, SeverityConfig{}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unsupported enrich processor config error = %v", err)
	}
	badProcessorConfig := source
	badProcessorConfig.Enrich.Processors = []EnrichProcessorConfig{{Type: "strategy", Config: map[string]any{"": "value"}}}
	if err := ValidateEventSources([]EventSource{badProcessorConfig}, SeverityConfig{}); err == nil || !strings.Contains(err.Error(), "config key") {
		t.Fatalf("invalid enrich processor config error = %v", err)
	}
}

func TestEventSourceFingerprintAndSeverity(t *testing.T) {
	t.Parallel()
	source := validEventSource()
	source.FingerprintMode = FingerprintModeFields
	source.FingerprintFields = []string{"subject_id", "dimensions.host"}
	source.SeverityMapping = map[string]string{"P1": "critical"}
	source.DefaultSeverity = "info"
	if err := ValidateEventSources([]EventSource{source}, SeverityConfig{}); err != nil {
		t.Fatalf("ValidateEventSources() error = %v", err)
	}
	if got, err := source.MapSeverity("P1", SeverityConfig{}); err != nil || got != "critical" {
		t.Fatalf("MapSeverity(P1) = %q, %v", got, err)
	}
	if got, err := source.MapSeverity("warning", SeverityConfig{}); err != nil || got != "warning" {
		t.Fatalf("MapSeverity(global name) = %q, %v", got, err)
	}
	if got, err := source.MapSeverity("unknown", SeverityConfig{}); err != nil || got != "info" {
		t.Fatalf("MapSeverity(fallback) = %q, %v", got, err)
	}
	globalFallback := source
	globalFallback.DefaultSeverity = ""
	if got, err := globalFallback.MapSeverity("unknown", SeverityConfig{}); err != nil || got != "warning" {
		t.Fatalf("MapSeverity(global fallback) = %q, %v", got, err)
	}
	bad := source
	bad.FingerprintFields = []string{"event_id"}
	if err := ValidateEventSources([]EventSource{bad}, SeverityConfig{}); err == nil {
		t.Fatal("unstable fingerprint field accepted")
	}
	bad = source
	bad.SeverityMapping = map[string]string{"P0": "unknown"}
	if err := ValidateEventSources([]EventSource{bad}, SeverityConfig{}); err == nil {
		t.Fatal("unknown mapped severity accepted")
	}
}

func TestEventSourceCloneAndRedaction(t *testing.T) {
	t.Parallel()
	source := validEventSource()
	source.FingerprintMode = FingerprintModeFields
	source.FingerprintField = ""
	source.FingerprintFields = []string{"source_alert_id", "dimensions.host"}
	source.SeverityMapping = map[string]string{"P1": "critical"}
	source.Storage.Kafka.Security.SASL = &kafkaclient.SASLConfig{Mechanism: "plain", Username: "user", Password: "secret"}
	source.Enrich.Processors = []EnrichProcessorConfig{{Type: "strategy"}}
	redacted := source.Redacted()
	redacted.Enrich.Processors[0].Type = "display"
	redacted.FingerprintFields[0] = "subject_id"
	redacted.SeverityMapping["P1"] = "info"
	redacted.Storage.Kafka.Brokers[0] = "changed"
	if reflect.DeepEqual(source, redacted) || source.Enrich.Processors[0].Type != "strategy" || source.FingerprintFields[0] != "source_alert_id" || source.SeverityMapping["P1"] != "critical" {
		t.Fatalf("Redacted changed original: %#v", source)
	}
}

func TestEnrichAcceptsTopologyIndexPrefix(t *testing.T) {
	t.Parallel()
	var dataSource ResourcesConfig
	decoder := yaml.NewDecoder(strings.NewReader(`onemodel:
  addresses: [http://onemodel.example.com:9200]
  index_prefix: custom_base_
`))
	decoder.KnownFields(true)
	if err := decoder.Decode(&dataSource); err != nil {
		t.Fatal(err)
	}
	if dataSource.OneModel == nil || dataSource.OneModel.IndexPrefix != "custom_base_" {
		t.Fatalf("datasource=%#v", dataSource)
	}
}

func TestEnrichSelectsOnlyProcessorDependencies(t *testing.T) {
	t.Parallel()
	enrich := EnrichConfig{
		Processors: []EnrichProcessorConfig{{Type: "source"}},
	}
	selected, err := enrich.SelectResources(*validResourcesConfig())
	if err != nil {
		t.Fatal(err)
	}
	if selected.MySQL == nil || selected.OneModel != nil {
		t.Fatalf("SelectDataSources()=%#v", selected)
	}
}

func TestResourcesCloneAndRedact(t *testing.T) {
	resources := *validResourcesConfig()
	resources.OneModel.APIKey = "api-secret"
	resources.KingeyeDisplay = &DisplayResource{Redis: RedisConfig{Address: "localhost:6379", Password: "redis-secret"}}
	cfg := Config{Resources: resources}
	redacted := cfg.Redacted().Resources
	redacted.MySQL.Database = "changed"
	redacted.OneModel.Addresses[0] = "http://changed:9200"
	if resources.MySQL.Database != "kingeye" || resources.OneModel.Addresses[0] != "http://onemodel.example.com:9200" {
		t.Fatal("shared resource clone")
	}
	if redacted.MySQL.Password != redactedSecret || redacted.OneModel.APIKey != redactedSecret || redacted.KingeyeDisplay.Redis.Password != redactedSecret {
		t.Fatal("resource secret exposed")
	}
}

func validResourcesConfig() *ResourcesConfig {
	return &ResourcesConfig{
		MySQL:    &MySQLResource{Address: "mysql.example.com:3306", Database: "kingeye", Username: "reader", Password: "secret"},
		OneModel: &OneModelResource{Addresses: []string{"http://onemodel.example.com:9200"}},
	}
}

func validEventSource() EventSource {
	return EventSource{EventSourceID: "source-a", Enabled: true, Cleaner: CleanerConfig{Type: CleanerTypeStandard}, Storage: EventSourceStorageConfig{Type: StorageTypeKafka, Kafka: KafkaStorageConfig{Brokers: []string{"kafka.example.com:9092"}, Topic: "alerts", ConsumerGroup: "linkd"}}}
}

func TestTestEnrichDoesNotRequireDataSources(t *testing.T) {
	source := validEventSource()
	source.Enrich = EnrichConfig{Processors: []EnrichProcessorConfig{{Type: "test", Config: map[string]any{"fields": map[string]any{"sample": true}, "datasource": map[string]any{"sleep_mean_milliseconds": 10, "sleep_stddev_milliseconds": 2, "error_rate": 0.1}}}}}
	if err := ValidateEventSources([]EventSource{source}, SeverityConfig{}); err != nil {
		t.Fatal(err)
	}
	selected, err := source.Enrich.SelectResources(ResourcesConfig{})
	if err != nil || selected.MySQL != nil || selected.OneModel != nil {
		t.Fatalf("selected=%#v error=%v", selected, err)
	}
}

func TestCustomEnrichCompileReleaseAndSecretBoundary(t *testing.T) {
	var enrich EnrichConfig
	if err := json.Unmarshal([]byte(`{"processors":[{"type":"fields","config":{"rules":[{"id":"x","operations":[{"id":"a","type":"assign","assignments":[{"target":"$.labels.strategy_id","value":{"literal":9001}}]}]}]}}]}`), &enrich); err != nil {
		t.Fatal(err)
	}
	if err := enrich.Validate(); err != nil {
		t.Fatal(err)
	}
	source := validEventSource()
	source.Enrich = enrich
	if err := ValidateEventSources([]EventSource{source}, SeverityConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"$.severity", "$.extra_data.cw_labels", "$.labels.dynamic_group_id"} {
		var invalid EnrichConfig
		raw, _ := json.Marshal(enrich)
		raw = []byte(strings.ReplaceAll(string(raw), "$.labels.strategy_id", target))
		if err := json.Unmarshal(raw, &invalid); err != nil {
			t.Fatal(err)
		}
		if err := invalid.Validate(); err == nil {
			t.Errorf("accepted %s", target)
		}
	}
}
