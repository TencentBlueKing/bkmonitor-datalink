// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
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
	"strings"
	"testing"
	"time"

	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/telemetry"
)

func TestAllInOneCommandStartsAndGracefullyStopsServices(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, allInOneConfig)
	started := make(chan string, 2)
	cleanerFactory := cleaner.FlowFactoryFunc(func(
		_ context.Context,
		_ config.EventSource,
	) (cleaner.Flow, error) {
		return cleaner.FlowFunc(func(ctx context.Context) error {
			started <- "cleaner"
			<-ctx.Done()
			return ctx.Err()
		}), nil
	})
	lifecycleRunner := LifecycleRunner(func(
		ctx context.Context,
		_ config.Config,
		_ *slog.Logger,
		_ *telemetry.Runtime,
	) error {
		started <- "lifecycle"
		<-ctx.Done()
		return ctx.Err()
	})
	stdout := &bytes.Buffer{}
	command := NewRootCommand("test-version", Dependencies{
		CleanerFlowFactory: cleanerFactory,
		LifecycleRunner:    lifecycleRunner,
	})
	command.SetOut(stdout)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"run", "all-in-one", "--config", path})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	waitNames(t, started, 2)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ExecuteContext() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("all-in-one command did not stop")
	}
	output := stdout.String()
	for _, message := range []string{
		"linkd all-in-one started",
		"linkd cleaner started",
		"linkd cleaner stopped",
		"linkd all-in-one stopped",
	} {
		if !strings.Contains(output, message) {
			t.Fatalf("all-in-one output missing %q: %s", message, output)
		}
	}
}

func TestAllInOneCommandValidatesEveryServiceBeforeStarting(t *testing.T) {
	t.Parallel()

	path := writeCLIConfig(t, "{}\n")
	command, _, _ := testCommand("test-version")
	command.SetArgs([]string{"run", "all-in-one", "--config", path})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "lifecycle config is required") {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func waitNames(t *testing.T, values <-chan string, count int) map[string]bool {
	t.Helper()
	result := make(map[string]bool, count)
	for range count {
		select {
		case value := <-values:
			result[value] = true
		case <-time.After(time.Second):
			t.Fatalf("received %d of %d service notifications", len(result), count)
		}
	}
	return result
}

const allInOneConfig = `logging:
  level: info
  format: json
storage:
  repository: mysql
  mysql:
    address: mysql.example.com:3306
    database: linkd
    username: linkd
  redis:
    address: redis.example.com:6379
    database: 0
lifecycle:
  output:
    kafka:
      brokers: [kafka.example.com:9092]
      topic: linkd-alerts
event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka.example.com:9092]
        topic: raw-source-a
        consumer_group: linkd-source-a
`
