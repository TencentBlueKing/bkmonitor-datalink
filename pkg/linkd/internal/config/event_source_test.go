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
	"reflect"
	"strings"
	"testing"

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
}

func TestEventSourceFingerprintAndSeverity(t *testing.T) {
	t.Parallel()
	source := validEventSource()
	source.FingerprintMode = FingerprintModeFields
	source.FingerprintFields = []string{"condition_key", "dimensions.host"}
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
	redacted := source.Redacted()
	redacted.FingerprintFields[0] = "condition_key"
	redacted.SeverityMapping["P1"] = "info"
	redacted.Storage.Kafka.Brokers[0] = "changed"
	if reflect.DeepEqual(source, redacted) || source.FingerprintFields[0] != "source_alert_id" || source.SeverityMapping["P1"] != "critical" {
		t.Fatalf("Redacted changed original: %#v", source)
	}
}

func validEventSource() EventSource {
	return EventSource{EventSourceID: "source-a", Enabled: true, Cleaner: CleanerConfig{Type: CleanerTypeStandard}, Storage: EventSourceStorageConfig{Type: StorageTypeKafka, Kafka: KafkaStorageConfig{Brokers: []string{"kafka.example.com:9092"}, Topic: "alerts", ConsumerGroup: "linkd"}}}
}
