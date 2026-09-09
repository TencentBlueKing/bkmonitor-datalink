// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
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
	"io"
	"strings"
	"testing"

	linkdconfig "linkd/internal/config"
	"linkd/internal/eventgen"
	"linkd/internal/kafkaclient"
	"linkd/internal/logging"
)

type fakeManagedPublisher struct {
	records []eventgen.Record
	closed  bool
}

func (p *fakeManagedPublisher) Publish(_ context.Context, records []eventgen.Record) error {
	p.records = append(p.records, records...)
	return nil
}

func (p *fakeManagedPublisher) Close() { p.closed = true }

func TestCommandVersionDoesNotRequireRuntimeDependencies(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, version, commit, want string
	}{
		{"release", "0.1.0", "abcdef0123456789", "version: 0.1.0\ngit_commit: abcdef0123456789\n"},
		{"development", "", "", "version: dev\ngit_commit: unknown\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := newRootCommand(tc.version, tc.commit, dependencies{})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetArgs([]string{"version"})
			if err := command.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if output.String() != tc.want {
				t.Fatalf("version output = %q, want %q", output.String(), tc.want)
			}
		})
	}
}

func TestCommandVersionRejectsArgumentsAndPropagatesWriteErrors(t *testing.T) {
	t.Parallel()
	command := newRootCommand("0.1.0", "test-commit", dependencies{})
	command.SetArgs([]string{"version", "extra"})
	if err := command.ExecuteContext(context.Background()); err == nil {
		t.Fatal("version accepted an unexpected argument")
	}
	command = newRootCommand("0.1.0", "test-commit", dependencies{})
	command.SetOut(failingVersionWriter{})
	command.SetArgs([]string{"version"})
	if err := command.ExecuteContext(context.Background()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("version write error = %v, want closed pipe", err)
	}
}

type failingVersionWriter struct{}

func (failingVersionWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCommandRunsFiniteGenerator(t *testing.T) {
	t.Parallel()
	publisher := &fakeManagedPublisher{}
	deps := testDependencies(publisher)
	command := newRootCommand("test-version", "test-commit", deps)
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{
		"--config", "test.yaml",
		"--event-source-id", "source-a",
		"--tenant-id", "tenant-a",
		"--new-alerts-per-minute", "20",
		"--cycle-duration", "30s",
		"--mean-lifetime-cycles", "1",
		"--duplicate-percent", "100",
		"--scenarios", "cpu_high,disk_full",
		"--seed", "42",
		"--max-active-alerts", "100",
		"--cycles", "1",
	})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(publisher.records) != 20 {
		t.Fatalf("published records = %d, want 20", len(publisher.records))
	}
	if !publisher.closed {
		t.Fatal("publisher was not closed")
	}
	if output := stderr.String(); !strings.Contains(output, "event generator started") ||
		!strings.Contains(output, "seed=42") || !strings.Contains(output, "generated=10") {
		t.Fatalf("stderr = %q", output)
	}
}

func TestCommandValidatesRequiredSelection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      []string
		wantError string
	}{
		{name: "event source", args: []string{"--tenant-id", "tenant-a", "--cycles", "1"}, wantError: "event_source_id is required"},
		{name: "tenant", args: []string{"--event-source-id", "source-a", "--cycles", "1"}, wantError: "tenant_id is required"},
		{
			name: "scenario", args: []string{
				"--event-source-id", "source-a", "--tenant-id", "tenant-a", "--scenarios", "unknown", "--cycles", "1",
			}, wantError: "unsupported scenario",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := newRootCommand("test-version", "test-commit", testDependencies(&fakeManagedPublisher{}))
			command.SetArgs(test.args)
			err := command.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ExecuteContext() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestCommandDefaults(t *testing.T) {
	t.Parallel()
	command := newRootCommand("test-version", "test-commit", testDependencies(&fakeManagedPublisher{}))
	for name, want := range map[string]string{
		"config":                "./configs/linkd.yaml",
		"new-alerts-per-minute": "20",
		"cycle-duration":        "30s",
		"mean-lifetime-cycles":  "4",
		"duplicate-percent":     "0",
		"max-active-alerts":     "100000",
		"cycles":                "0",
	} {
		flag := command.Flags().Lookup(name)
		if flag == nil || flag.DefValue != want {
			t.Fatalf("flag %q default = %#v, want %q", name, flag, want)
		}
	}
}

func testDependencies(publisher managedPublisher) dependencies {
	return dependencies{
		loadConfig: func(string) (linkdconfig.Config, error) {
			cfg := linkdconfig.Default()
			cfg.Logging = logging.Config{Level: logging.LevelInfo, Format: logging.FormatText}
			cfg.EventSources = []linkdconfig.EventSource{{
				EventSourceID: "source-a", Enabled: true,
				Cleaner:         linkdconfig.CleanerConfig{Type: linkdconfig.CleanerTypeStandard},
				FingerprintMode: linkdconfig.FingerprintModeField, FingerprintField: "source_alert_id",
				Storage: linkdconfig.EventSourceStorageConfig{
					Type: linkdconfig.StorageTypeKafka,
					Kafka: linkdconfig.KafkaStorageConfig{
						Brokers: []string{"127.0.0.1:9092"}, Topic: "events", ConsumerGroup: "cleaner",
						Security: kafkaclient.SecurityConfig{Protocol: kafkaclient.SecurityProtocolPlaintext},
					},
				},
			}}
			return cfg, nil
		},
		newPublisher: func(linkdconfig.EventSource) (managedPublisher, error) { return publisher, nil },
		newRunID:     func() (string, error) { return "test-run", nil },
		resolveSeed: func(seed uint64) (uint64, error) {
			if seed == 0 {
				return 42, nil
			}
			return seed, nil
		},
	}
}
