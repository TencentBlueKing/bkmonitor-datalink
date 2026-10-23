// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/go-redis/redis/v8"
)

func openRoleRuntimeForTest(t *testing.T, cfg config.Config) *phaseTwoRoleRuntime {
	t.Helper()
	runtime, err := openPhaseTwoRoleRuntime(cfg, metric.NewRecorder(metric.BuildInfo{}), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background(), newPhaseTwoApplicationHealth())
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	waitPhaseTwoCondition(t, 3*time.Second, "role instance registration", func() bool {
		instance, found, err := runtime.store.ReadInstance(context.Background(), cfg.PhaseTwo.Worker.ID)
		return err == nil && found && instance.Incarnation == runtime.Incarnation
	})
	return runtime
}

func openSplitControlForFullPipeline(t *testing.T, cfg config.Config, now func() time.Time,
	observer observability.Observer, client *http.Client) *phaseTwoWorkerBundle {
	t.Helper()
	cfg.Roles = roles.Set{roles.Control}
	cfg.PhaseTwo.Worker.ID = "full-pipeline-control"
	cfg.PhaseTwo.Coordinator.MaxEvents++ // Control's resources differ from the Worker's.
	cfg.PhaseTwo.Access.UQEndpoint = ""
	runtime := openRoleRuntimeForTest(t, cfg)
	control, err := openProductionPhaseTwoBundleWithDependencies(context.Background(), cfg,
		metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{RoleRuntime: runtime, Now: now, HTTPClient: client, AdditionalObserver: observer,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := control.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		if stream, found := controlStreamServers.LoadAndDelete(control); found {
			stream.(*testControlStream).stop()
		}
	})
	if err := control.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if control.workerPorts.Query != nil || len(control.runners) != 0 || control.dependencies.ViewClient != nil {
		t.Fatal("control-only factory assembled Worker execution")
	}
	serveControlStreamForTest(control)
	return control
}
