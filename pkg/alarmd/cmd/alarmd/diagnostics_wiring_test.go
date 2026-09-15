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
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The phase-one runtime is a supported input mode, not dead code, so it needs
// the same protection as phase two: dropping the address there would compile,
// keep every test green, and leave that mode without pprof.
func TestPhaseOneRuntimePassesTheConfiguredDiagnosticsAddress(t *testing.T) {
	cfg := validApplicationConfig()
	cfg.HTTP.DiagnosticsListen = "127.0.0.1:16061"
	ctx, cancel := context.WithCancel(context.Background())
	serviceStarted := make(chan struct{})
	observed := make(chan string, 1)
	service := newFakeServiceRuntime()
	service.run = func(ctx context.Context) error {
		close(serviceStarted)
		<-ctx.Done()
		return nil
	}
	bundle := &applicationBundle{service: service, gate: coordinator.NewCriticalDependencyGate(nil)}
	dependencies := applicationDependencies{
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error) {
			return bundle, nil
		},
		newHTTP: func(_ *metric.Recorder, _ observability.HealthSource, diagnosticsAddress string) (httpRuntime, error) {
			observed <- diagnosticsAddress
			return &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
				<-ctx.Done()
				return nil
			}}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- runApplication(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), dependencies)
	}()
	<-serviceStarted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runApplication() error = %v", err)
	}

	select {
	case address := <-observed:
		if address != cfg.HTTP.DiagnosticsListen {
			t.Fatalf("diagnostics address = %q, want %q", address, cfg.HTTP.DiagnosticsListen)
		}
	default:
		t.Fatal("HTTP service was never constructed")
	}
}

// Without this assertion the diagnostics address could be dropped on its way
// from configuration to the HTTP service and every test would still pass, while
// production quietly serves no pprof at all.
func TestPhaseTwoRuntimePassesTheConfiguredDiagnosticsAddress(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.HTTP.DiagnosticsListen = "127.0.0.1:16060"
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	observed := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	dependencies := phaseTwoApplicationDependencies{
		configureCPU: func() (string, error) { return "cpu_quota", nil },
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger, *phaseTwoApplicationHealth) (*phaseTwoWorkerBundle, error) {
			return bundle, nil
		},
		newHTTP: func(_ *metric.Recorder, _ observability.HealthSource, diagnosticsAddress string) (httpRuntime, error) {
			observed <- diagnosticsAddress
			return &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
				<-ctx.Done()
				return nil
			}}, nil
		},
	}
	// The startup fact is captured here too: without it, deleting the field
	// would leave every gate green while the surface becomes unreportable.
	var startupLog bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runPhaseTwoApplicationWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.New(observability.ComponentRuntime, &startupLog), dependencies)
	}()
	waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runPhaseTwoApplicationWithDependencies() error = %v", err)
	}

	select {
	case address := <-observed:
		if address != cfg.HTTP.DiagnosticsListen {
			t.Fatalf("diagnostics address = %q, want %q", address, cfg.HTTP.DiagnosticsListen)
		}
	default:
		t.Fatal("HTTP service was never constructed")
	}
	if !strings.Contains(startupLog.String(), `"diagnostics_listen":"127.0.0.1:16060"`) {
		t.Fatalf("startup log does not report the diagnostics surface: %s", startupLog.String())
	}
}

// An unset address must still be reported, or the capability disappears with no
// signal at all -- the failure mode the split exists to prevent.
func TestDisabledDiagnosticsSurfaceIsStillReported(t *testing.T) {
	if fact := (config.HTTPConfig{}).DiagnosticsFact(); fact != "disabled" {
		t.Fatalf("unset diagnostics fact = %q, want %q", fact, "disabled")
	}
	if fact := (config.HTTPConfig{DiagnosticsListen: "127.0.0.1:6060"}).DiagnosticsFact(); fact != "127.0.0.1:6060" {
		t.Fatalf("set diagnostics fact = %q, want the address", fact)
	}
}
