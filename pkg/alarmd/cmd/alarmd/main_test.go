// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestRunPrintsVersionWithoutLoadingConfiguration(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--version", "--config", "/missing/config.yaml"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"alarmd", "version=", "commit=", "schema_version="} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("version output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestRunChecksConfigurationWithoutStartingApplication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	if err := os.WriteFile(path, []byte(validApplicationYAML()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	want := errors.New("application must not start")
	dependencies := runtimeModeDependencies{
		phaseTwo: phaseTwoApplicationDependencies{
			run: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) error {
				return want
			},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithRuntimeModeDependencies(
		context.Background(), []string{"--check-config", "--config", path}, &stdout, &stderr, dependencies,
	)
	if code != 0 {
		t.Fatalf("runWithRuntimeModeDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), want.Error()) {
		t.Fatalf("runWithRuntimeModeDependencies() started application: %q", stderr.String())
	}
}

func TestRunCheckConfigurationRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	contents := validApplicationYAML() + "unknown_field: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"--check-config", "--config", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "field unknown_field not found") {
		t.Fatalf("run() stderr = %q, want strict field error", stderr.String())
	}
}

func TestRunRejectsConflictingTerminalFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--version", "--check-config"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot be used together") {
		t.Fatalf("run() stderr = %q, want flag conflict", stderr.String())
	}
}

func TestRunRejectsOwnerConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	if err := os.WriteFile(path, []byte("mode: owner\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"--config", path}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run() accepted owner configuration, stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "mode") {
		t.Fatalf("run() stderr = %q, want mode context", stderr.String())
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--unknown"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("run() accepted an unknown flag")
	}
	if stderr.Len() == 0 {
		t.Fatal("run() did not report flag error")
	}
}

func validApplicationYAML() string {
	return `input:
  mode: phase_one_kafka_compatibility
  phase_one_kafka:
    input_topic: alarmd-shadow-input-v2
    consumer_group: alarmd-shadow-v2
    initial_offset: oldest
    state_prefix: alarmd-shadow
http:
  listen: 127.0.0.1:8080
shutdown_timeout: 1s
kafka:
  brokers:
    - 127.0.0.1:9092
  trigger_event:
    topic: alarmd-shadow-trigger-event-v1
    max_message_bytes: 524288
  message_receipt:
    topic: alarmd-shadow-message-receipt-v1
    max_message_bytes: 524288
  allowed_output_topics:
    - alarmd-shadow-trigger-event-v1
    - alarmd-shadow-message-receipt-v1
    - alarmd_0bkmonitor_backend_event
  legacy_adapter:
    topic: alarmd_0bkmonitor_backend_event
    snapshot_prefix: alarmd-compatibility-test
    service_redis:
      mode: standalone
      address: 127.0.0.1:6379
redis:
  address: 127.0.0.1:6379
`
}

func validGoAccessApplicationYAML() string {
	return `http:
  listen: 127.0.0.1:8080
shutdown_timeout: 1s
kafka:
  brokers:
    - 127.0.0.1:9092
  trigger_event:
    topic: alarmd-shadow-trigger-event-v2
    max_message_bytes: 524288
  allowed_output_topics:
    - alarmd-shadow-trigger-event-v2
    - alarmd_0bkmonitor_backend_event
  legacy_adapter:
    topic: alarmd_0bkmonitor_backend_event
    snapshot_prefix: alarmd-compatibility-test
    service_redis:
      mode: standalone
      address: 127.0.0.1:6379
redis:
  address: 127.0.0.1:6379
  state_prefix: alarmd-phase-two
phase_two:
  worker:
    id: alarmd-worker-0
  control:
    strategy_cache_prefix: alarm-config
    timezone: Asia/Shanghai
    legacy_query_runtime:
      access_bk_data: false
      bkdata_cmdb_level_tables: []
      system_disk_filter:
        field_name: device_type
        values: []
  access:
    uq_endpoint: http://unify-query.service
    query_source: alarmd
`
}
