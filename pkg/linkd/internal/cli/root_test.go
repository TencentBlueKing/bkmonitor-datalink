// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/telemetry"
)

func TestRootHelpDoesNotLoadConfig(t *testing.T) {
	t.Parallel()

	command, stdout, _ := testCommand("test-version")
	command.SetArgs(nil)
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Linkd 告警处理服务") ||
		!strings.Contains(stdout.String(), "run") ||
		!strings.Contains(stdout.String(), "storage") ||
		strings.Contains(stdout.String(), "all-in-one") ||
		strings.Contains(stdout.String(), "control-plane") ||
		strings.Contains(stdout.String(), "storage-manager") ||
		strings.Contains(stdout.String(), "serve") {
		t.Fatalf("root help = %s", stdout.String())
	}
}

func TestRunHelpListsProcessRoles(t *testing.T) {
	t.Parallel()

	command, stdout, _ := testCommand("test-version")
	command.SetArgs([]string{"run", "--help"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	for _, role := range []string{"all-in-one", "cleaner", "lifecycle", "control-plane"} {
		if !strings.Contains(stdout.String(), role) {
			t.Fatalf("run help missing %q: %s", role, stdout.String())
		}
	}
}

func TestLifecycleCommandDelegatesToRunner(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, `lifecycle:
  concurrency: 3
  output:
    kafka:
      brokers: [kafka.example.com:9092]
      topic: linkd-alerts
`)
	var calls atomic.Int32
	runner := LifecycleRunner(func(
		_ context.Context,
		cfg config.Config,
		_ *slog.Logger,
		_ *telemetry.Runtime,
	) error {
		calls.Add(1)
		if cfg.Lifecycle == nil || cfg.Lifecycle.Concurrency != 3 {
			t.Fatalf("runner config = %#v", cfg.Lifecycle)
		}
		return nil
	})
	stdout := &bytes.Buffer{}
	command := NewRootCommand("test-version", Dependencies{LifecycleRunner: runner})
	command.SetOut(stdout)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"run", "lifecycle", "--config", path})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("lifecycle runner calls = %d", calls.Load())
	}
}

func TestLifecycleCommandRequiresLifecycleConfig(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, "{}\n")
	command, _, _ := testCommand("test-version")
	command.SetArgs([]string{"run", "lifecycle", "--config", path})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "lifecycle config is required") {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func TestControlPlaneRequiresManagementTask(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, "{}\n")
	command, _, _ := testCommand("test-version")
	command.SetArgs([]string{"run", "control-plane", "--config", path})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no management tasks are enabled") {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	t.Parallel()

	command := NewRootCommand("test-version", Dependencies{})
	flag := command.PersistentFlags().Lookup("config")
	if flag == nil || flag.DefValue != defaultConfigPath {
		t.Fatalf("config default = %#v, want %q", flag, defaultConfigPath)
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, "{}\n")
	command, stdout, _ := testCommand("test-version")
	command.SetArgs([]string{"config", "validate", "--config", path})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if stdout.String() != "configuration is valid\n" {
		t.Fatalf("validate output = %q", stdout.String())
	}
}

func TestConfigValidateRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, "version: 1\n")
	command, _, _ := testCommand("test-version")
	command.SetArgs([]string{"config", "validate", "--config", path})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "field version not found") {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func TestConfigPrintUsesEffectiveValues(t *testing.T) {
	path := writeCLIConfig(t, `logging:
  level: debug
  format: text
`)
	t.Setenv("LINKD_LOG_LEVEL", "warn")
	t.Setenv("LINKD_LOG_FORMAT", "json")

	command, stdout, _ := testCommand("test-version")
	command.SetArgs([]string{"config", "print", "--config", path, "--log-level", "error"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	if strings.Contains(stdout.String(), "version:") || !strings.Contains(stdout.String(), "default_severity: warning") || !strings.Contains(stdout.String(), "level: error") {
		t.Fatalf("print output = %q", stdout.String())
	}
}

func TestExplicitEmptyCLIOverrideIsValidated(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, "{}\n")
	command, _, _ := testCommand("test-version")
	command.SetArgs([]string{"config", "validate", "--config", path, "--log-level="})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "logging.level") {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	command, stdout, _ := testCommand("1.2.3")
	command.SetArgs([]string{"version"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if stdout.String() != "1.2.3\n" {
		t.Fatalf("version output = %q", stdout.String())
	}
}

func TestCompletionCommand(t *testing.T) {
	t.Parallel()

	command, stdout, _ := testCommand("test-version")
	command.SetArgs([]string{"completion", "bash"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "__start_linkd") {
		t.Fatalf("completion output does not contain linkd function")
	}
}

func TestCleanerStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, `logging:
  level: info
  format: json
`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	command, stdout, _ := testCommand("test-version")
	command.SetArgs([]string{"run", "cleaner", "--config", path})
	if err := command.ExecuteContext(ctx); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if output := stdout.String(); !strings.Contains(output, `"msg":"linkd cleaner started"`) ||
		!strings.Contains(output, `"msg":"linkd cleaner stopped"`) {
		t.Fatalf("cleaner output = %s", output)
	}
}

func TestCleanerStartsOneFlowPerEnabledEventSource(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, `event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka-a.example.com:9092]
        topic: raw-a
        consumer_group: cleaner-a
  - event_source_id: source-b
    enabled: false
    storage:
      type: kafka
      kafka:
        brokers: [kafka-b.example.com:9092]
        topic: raw-b
        consumer_group: cleaner-b
  - event_source_id: source-c
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka-c.example.com:9092]
        topic: raw-c
        consumer_group: cleaner-c
`)
	started := make(chan string, 3)
	factory := cleaner.FlowFactoryFunc(func(_ context.Context, source config.EventSource) (cleaner.Flow, error) {
		return cleaner.FlowFunc(func(ctx context.Context) error {
			started <- source.EventSourceID
			<-ctx.Done()
			return ctx.Err()
		}), nil
	})
	command, _, _ := testCommandWithCleaner("test-version", factory)
	command.SetArgs([]string{"run", "cleaner", "--config", path})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- command.ExecuteContext(ctx)
	}()

	got := map[string]int{}
	for range 2 {
		select {
		case eventSourceID := <-started:
			got[eventSourceID]++
		case <-time.After(time.Second):
			t.Fatal("cleaner flows did not start")
		}
	}
	if got["source-a"] != 1 || got["source-c"] != 1 || got["source-b"] != 0 {
		t.Fatalf("started cleaner flows = %v", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ExecuteContext() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cleaner command did not stop")
	}
}

func TestCleanerDefaultRunnerRequiresStorageForEnabledSource(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, `event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka.example.com:9092]
        topic: raw-a
        consumer_group: cleaner-a
`)
	command, _, _ := testCommand("test-version")
	command.SetArgs([]string{"run", "cleaner", "--config", path})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "storage config is required") {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func TestServeCommandIsRemoved(t *testing.T) {
	t.Parallel()

	command, _, _ := testCommand("test-version")
	command.SetArgs([]string{"serve"})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), `unknown command "serve"`) {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func TestStorageManagerCommandIsRemoved(t *testing.T) {
	t.Parallel()

	command, _, _ := testCommand("test-version")
	command.SetArgs([]string{"storage-manager"})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), `unknown command "storage-manager"`) {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func TestProcessCommandsAreOnlyAvailableUnderRun(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"all-in-one", "cleaner", "lifecycle", "control-plane"} {
		role := role
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			command, _, _ := testCommand("test-version")
			command.SetArgs([]string{role})
			err := command.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), `unknown command "`+role+`"`) {
				t.Fatalf("ExecuteContext() error = %v", err)
			}
		})
	}
}

func TestStoragePrepareCommandUsesAdministrativeHierarchy(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, `storage:
  repository: elasticsearch
  elasticsearch:
    addresses: [http://elasticsearch.example.com:9200]
    index_prefix: linkd-test
`)
	command, _, _ := testCommand("test-version")
	command.SetArgs([]string{
		"storage", "prepare", "--config", path,
		"--from", "invalid", "--to", "2026-09-02T00:00:00Z",
	})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "parse --from as RFC3339") {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func testCommand(version string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	return testCommandWithCleaner(version, nil)
}

func testCommandWithCleaner(
	version string,
	cleanerFactory cleaner.FlowFactory,
) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command := NewRootCommand(version, Dependencies{CleanerFlowFactory: cleanerFactory})
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command, stdout, stderr
}

func writeCLIConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "linkd.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
