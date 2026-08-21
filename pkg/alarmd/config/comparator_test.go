// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadComparatorDerivesConsumerAndAuditCoordinatesFromOneKafkaBlock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "comparator.yaml")
	content := []byte(`mode: shadow
http:
  listen: 127.0.0.1:8081
kafka:
  brokers: [127.0.0.1:9092]
  trigger_input_topic: trigger-input
  go_decision_topic: go-decision
  python_decision_topic: python-decision
  audit_output_topic: comparison-audit
  allowed_audit_output_topics: [comparison-audit]
  group_id: alarmd-comparator
  client_id: alarmd-comparator
  broker_version: 2.6.0
  max_entries: 1000
  coverage_timeout: 1m
  barrier_interval: 1s
shutdown_timeout: 10s
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	config, err := LoadComparator(path)
	if err != nil {
		t.Fatalf("LoadComparator() error = %v", err)
	}
	if got, want := config.Kafka.ServiceCoordinates().Topics(), []string{"trigger-input", "go-decision", "python-decision"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("service topics = %v, want %v", got, want)
	}
	if got := config.Kafka.AuditSinkCoordinates(); got.OutputTopic != "comparison-audit" || !reflect.DeepEqual(got.InputTopics, []string{"trigger-input", "go-decision", "python-decision"}) {
		t.Fatalf("audit coordinates = %#v", got)
	}
}
