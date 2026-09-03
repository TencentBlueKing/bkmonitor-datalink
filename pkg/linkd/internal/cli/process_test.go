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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/telemetry"
)

func TestStandaloneProcessCommandsStartRoleMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command string
		role    telemetry.Role
		set     func(*Dependencies, ProcessRunner)
	}{
		{
			command: "cleaner",
			role:    telemetry.RoleCleaner,
			set: func(dependencies *Dependencies, runner ProcessRunner) {
				dependencies.CleanerRunner = runner
			},
		},
		{
			command: "lifecycle",
			role:    telemetry.RoleLifecycle,
			set: func(dependencies *Dependencies, runner ProcessRunner) {
				dependencies.LifecycleRunner = runner
			},
		},
		{
			command: "control-plane",
			role:    telemetry.RoleControlPlane,
			set: func(dependencies *Dependencies, runner ProcessRunner) {
				dependencies.ControlPlaneRunner = runner
			},
		},
	}

	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			t.Parallel()

			path := writeCLIConfig(t, `telemetry:
  metrics:
    exporter: prometheus
    prometheus:
      listen_address: 127.0.0.1:0
`)
			runner := ProcessRunner(func(
				ctx context.Context,
				_ config.Config,
				_ *slog.Logger,
				runtime *telemetry.Runtime,
			) error {
				return assertProcessMetrics(ctx, runtime, test.role)
			})
			dependencies := Dependencies{}
			test.set(&dependencies, runner)
			command := NewRootCommand("test-version", dependencies)
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})
			command.SetArgs([]string{"run", test.command, "--config", path})
			if err := command.ExecuteContext(context.Background()); err != nil {
				if strings.Contains(err.Error(), "operation not permitted") {
					t.Skipf("sandbox does not allow local listeners: %v", err)
				}
				t.Fatalf("ExecuteContext() error = %v", err)
			}
		})
	}
}

func assertProcessMetrics(ctx context.Context, runtime *telemetry.Runtime, role telemetry.Role) error {
	if runtime == nil {
		return fmt.Errorf("telemetry runtime is nil")
	}
	address := runtime.PrometheusListenAddress()
	if address == "" {
		return fmt.Errorf("prometheus listener is not active")
	}
	counter, err := runtime.MeterProvider().Meter("linkd-cli-test").Int64Counter("linkd.command.test")
	if err != nil {
		return fmt.Errorf("create test counter: %w", err)
	}
	counter.Add(ctx, 1)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/metrics", nil)
	if err != nil {
		return fmt.Errorf("build metrics request: %w", err)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("scrape metrics: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read metrics: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("scrape metrics status = %d", response.StatusCode)
	}
	want := `linkd_role="` + string(role) + `"`
	if !strings.Contains(string(body), want) {
		return fmt.Errorf("metrics missing role %q", role)
	}
	return nil
}
