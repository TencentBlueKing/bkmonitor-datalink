// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	httpservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/service/http"
)

func withRoleAuth(cfg *config.Config) {
	cfg.CLI = config.CLIConfig{EnvironmentID: "role-fixture", EnvironmentName: "Role fixture",
		PublicBaseURL: "http://ob.example/alarmd", AdminKey: strings.Repeat("k", 32)}
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(2 * time.Second)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(20 * time.Millisecond)
}

func TestRoleAuthReadinessIsIndependentOfLegacyBusinessReadiness(t *testing.T) {
	for _, selected := range []roles.Set{{roles.Channel}, nil} {
		t.Run(selected.String(), func(t *testing.T) {
			address, _ := startPhaseTwoRedis(t)
			cfg := validGoAccessRuntimeConfig()
			cfg.Redis.Address, cfg.Roles = address, selected
			withRoleAuth(&cfg)
			app, err := newPhaseTwoApplication(cfg)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := openPhaseTwoRoleRuntime(cfg, metric.NewRecorder(metric.BuildInfo{}), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			var unavailable atomic.Bool
			probe := runtime.Channel.Ready
			runtime.Channel.Ready = func(ctx context.Context) error {
				if unavailable.Load() {
					return errors.New("auth_store_unavailable")
				}
				return probe(ctx)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runtime.Start(ctx, app.health)
			waitPhaseTwoCondition(t, 3*time.Second, "channel auth ready", func() bool {
				return app.health.channelReadiness() == observability.HealthReady && (selected == nil || app.HealthSnapshot().Ready)
			})
			if selected == nil {
				app.health.Update(phaseTwoReadiness{State: observability.HealthReady, SnapshotReady: true,
					AssignmentReady: true, RuntimeStateReady: true, OutputSinkReady: true})
			}
			if !app.HealthSnapshot().Ready {
				t.Fatal("ready authentication/store or legacy business was not ready")
			}
			unavailable.Store(true)
			waitPhaseTwoCondition(t, 3*time.Second, "auth dependency failure", func() bool {
				return app.health.channelReadiness() == observability.HealthNotReady && (selected == nil || !app.HealthSnapshot().Ready)
			})
			snapshot := app.HealthSnapshot()
			if snapshot.RoleReadiness["channel"] != "not_ready" || snapshot.Ready != (selected == nil) {
				t.Fatalf("channel failure changed the wrong readiness contract: %+v", snapshot)
			}
			unavailable.Store(false)
			waitPhaseTwoCondition(t, 3*time.Second, "auth dependency recovery", func() bool {
				return app.HealthSnapshot().Ready && app.health.channelReadiness() == observability.HealthReady
			})
			if _, found, err := runtime.store.ReadWorker(ctx, cfg.PhaseTwo.Worker.ID); err != nil || found {
				t.Fatalf("process registration manufactured a Worker: found=%v err=%v", found, err)
			}
		})
	}
}

type passiveRoleHTTP struct{ *httpservice.Server }

func (server *passiveRoleHTTP) Run(ctx context.Context, _ string, _ time.Duration) error {
	<-ctx.Done()
	return nil
}

func TestApplicationInstallsChannelWhileBusinessStartupWaits(t *testing.T) {
	address, _ := startPhaseTwoRedis(t)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withRoleAuth(&cfg)
	opened := make(chan *phaseTwoRoleRuntime, 1)
	waiting := make(chan struct{})
	listener := make(chan *httpservice.Server, 1)
	deps := phaseTwoApplicationDependencies{
		openRoles: func(cfg config.Config, recorder *metric.Recorder, facts *observability.RuntimeConfigFacts, logger *observability.Logger) (*phaseTwoRoleRuntime, error) {
			runtime, err := openPhaseTwoRoleRuntime(cfg, recorder, facts, logger)
			if err == nil {
				opened <- runtime
			}
			return runtime, err
		},
		openRoleBundle: func(ctx context.Context, _ config.Config, _ *metric.Recorder, _ *observability.Logger, _ *phaseTwoApplicationHealth, _ *phaseTwoRoleRuntime) (*phaseTwoWorkerBundle, error) {
			close(waiting)
			<-ctx.Done()
			return nil, ctx.Err()
		},
		newHTTP: func(recorder *metric.Recorder, source observability.HealthSource, _ httpSurface) (httpRuntime, error) {
			server, err := httpservice.NewWithHealth(recorder, source)
			if err == nil {
				listener <- server
			}
			return &passiveRoleHTTP{server}, err
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runPhaseTwoApplicationWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), nil, deps)
	}()
	server, runtime := <-listener, <-opened
	<-waiting
	request := httptest.NewRequest(http.MethodPost, "/api/cli/channel", strings.NewReader(`{"action":"discover"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("early channel not installed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("business-waiting process lost its legacy /readyz behavior")
	}
	waitPhaseTwoCondition(t, 3*time.Second, "early instance", func() bool {
		_, found, err := runtime.store.ReadInstance(ctx, cfg.PhaseTwo.Worker.ID)
		return err == nil && found
	})
	if _, found, err := runtime.store.ReadWorker(ctx, cfg.PhaseTwo.Worker.ID); err != nil || found {
		t.Fatalf("waiting business manufactured a Worker: found=%v err=%v", found, err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation = %v", err)
	}
}
