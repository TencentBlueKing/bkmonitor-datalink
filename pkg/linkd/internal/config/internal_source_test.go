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

	"linkd/internal/domain"
)

func TestInternalMergeSourceHasNoExternalInputDefaults(t *testing.T) {
	source := EventSource{EventSourceID: domain.BuiltinMergeEventSourceID, Enabled: true, Storage: EventSourceStorageConfig{Type: StorageTypeInternalMerge}}.WithDefaults()
	if err := ValidateEventSources([]EventSource{source}, DefaultSeverityConfig()); err != nil {
		t.Fatal(err)
	}
	if source.Cleaner.Type != "" || source.FingerprintMode != "" || source.Storage.Kafka.FetchMaxWaitMilliseconds != 0 || source.Scheduling.Cleaner.Replicas.Limit(10) != 0 || source.Scheduling.Lifecycle.Replicas.Limit(10) != 10 {
		t.Fatalf("external defaults leaked %+v", source)
	}
	raw, err := json.Marshal(source.Redacted())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"kafka"`) {
		t.Fatal("internal source serialized fake Kafka subscription")
	}
	for _, change := range []func(*EventSource){func(s *EventSource) { s.EventSourceID = "external" }, func(s *EventSource) { s.Cleaner.Type = "standard" }, func(s *EventSource) { s.Storage.Kafka.Topic = "fake" }, func(s *EventSource) { s.FingerprintField = "source_alert_id" }, func(s *EventSource) { n := 1; s.Scheduling.Cleaner.Replicas.Number = &n }} {
		bad := source
		change(&bad)
		if err := ValidateEventSources([]EventSource{bad}, DefaultSeverityConfig()); err == nil {
			t.Fatal("internal source accepted external input")
		}
	}
	external := validEventSource()
	external.EventSourceID = domain.BuiltinMergeEventSourceID
	if err := ValidateEventSources([]EventSource{external}, DefaultSeverityConfig()); err == nil {
		t.Fatal("external input occupied reserved source ID")
	}
}

func TestDecodeInternalMergeSourceWithoutKafkaBlock(t *testing.T) {
	enabled := true
	sources, err := decodeEventSources([]fileEventSource{{EventSourceID: domain.BuiltinMergeEventSourceID, Enabled: &enabled, Storage: &fileStorageConfig{Type: StorageTypeInternalMerge}}})
	if err != nil || len(sources) != 1 {
		t.Fatal("internal source required dummy Kafka", err)
	}
	if err := ValidateEventSources(sources, DefaultSeverityConfig()); err != nil {
		t.Fatal(err)
	}
}
