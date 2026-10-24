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
	"testing/fstest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/clirelease"
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
	if err := os.WriteFile(path, []byte(validGoAccessApplicationYAML()), 0o600); err != nil {
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

// An unknown key is ignored and named: check-config passes and says which.
func TestRunCheckConfigurationNamesAnIgnoredUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	contents := validGoAccessApplicationYAML() + "unknown_field: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"--check-config", "--config", path}, &stdout, &stderr)
	if code != checkConfigUnknownKeys {
		t.Fatalf("run() code = %d, want %d: a preflight does not pass a key nothing reads; stderr = %q", code, checkConfigUnknownKeys, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Fatal("the resolved facts were not printed beside the ignored key")
	}
	if !strings.Contains(stderr.String(), "ignored") || !strings.Contains(stderr.String(), "field unknown_field not found") {
		t.Fatalf("run() stderr = %q, want the ignored field named", stderr.String())
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

// A binary built without a release fails the build's own check, before
// reading any configuration. The release is injected: a local make build
// leaves one in the package's embed, and the test must not depend on that.
func TestCheckCLIReleaseFailsABinaryBuiltWithoutOne(t *testing.T) {
	embedded := embeddedCLIRelease
	t.Cleanup(func() { embeddedCLIRelease = embedded })
	embeddedCLIRelease = func() *clirelease.Release { return clirelease.LoadFS(fstest.MapFS{}, version) }
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--check-cli-release", "--config", "/missing/config.yaml"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "built without an alarmd-cli release") || stdout.Len() != 0 {
		t.Fatalf("run() code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	stderr.Reset()
	if code := run(context.Background(), []string{"--check-cli-release", "--version"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "cannot be used together") {
		t.Fatalf("run() code = %d, stderr = %q, want flag conflict", code, stderr.String())
	}
}

// The retired mode key never made a process an owner; a file that still says
// owner is the same process, with the key named as ignored.
func TestRunIgnoresAndNamesOwnerMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	if err := os.WriteFile(path, []byte(validGoAccessApplicationYAML()+"mode: owner\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"--check-config", "--config", path}, &stdout, &stderr); code != checkConfigUnknownKeys {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "field mode not found") {
		t.Fatalf("run() stderr = %q, want mode named as ignored", stderr.String())
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

func TestRunRoleFlagOverridesYAMLBeforeDependencyValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	contents := "roles: [control]\nredis:\n  address: 127.0.0.1:6379\nphase_two:\n  worker:\n    id: channel-fixture\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{"channel", "", "unknown", "worker,worker"} {
		t.Run(selected, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithRuntimeModeDependencies(context.Background(),
				[]string{"--check-config", "--config", path, "--roles=" + selected}, &stdout, &stderr, runtimeModeDependencies{})
			if selected == "channel" {
				if code != 0 {
					t.Fatalf("role override failed: code=%d stderr=%s", code, &stderr)
				}
			} else if code != 2 || !strings.Contains(stderr.String(), "roles") {
				t.Fatalf("invalid roles were accepted: code=%d stderr=%s", code, &stderr)
			}
		})
	}
}

func validGoAccessApplicationYAML() string {
	return `http:
  listen: 127.0.0.1:8080
kafka:
  brokers:
    - 127.0.0.1:9092
  trigger_event:
    topic: alarmd-shadow-trigger-event-v2
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
  access:
    uq_endpoint: http://unify-query.service
    query_source: alarmd
`
}
