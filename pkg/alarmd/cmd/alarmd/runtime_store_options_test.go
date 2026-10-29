// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The execution store production opens, both of them, takes the
// deployment's no-data group bound from the key an operator writes, and the
// store's default when the key is absent.
func TestTheExecutionStoreTakesTheDeploymentsNoDataGroupBound(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	if got := executionStoreOptions(cfg, nil).MaxNoDataGroups; got != 0 {
		t.Fatalf("a deployment stating no bound gives the store %d, want 0 for the store's default", got)
	}
	bound := 3
	cfg.PhaseTwo.NoData.MaxGroups = &bound
	if got := executionStoreOptions(cfg, nil).MaxNoDataGroups; got != 3 {
		t.Fatalf("phase_two.no_data.max_groups 3 gives the store %d", got)
	}
}

// A deployment bound above the default writes one startup line naming the
// bound, the default and the measured one-off cost; at the default, or with
// no bound stated, startup says nothing about it.
func TestADeploymentNoDataGroupBoundAboveTheDefaultIsLoggedOnceAtStartup(t *testing.T) {
	for name, test := range map[string]struct {
		bound  int
		logged bool
	}{
		"no bound stated":       {},
		"at the default":        {bound: 100000},
		"one above the default": {bound: 100001, logged: true},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validGoAccessRuntimeConfig()
			if test.bound > 0 {
				cfg.PhaseTwo.NoData.MaxGroups = &test.bound
			}
			var out bytes.Buffer
			logNoDataGroupBound(observability.New("alarmd", &out), cfg)
			lines := strings.Count(out.String(), "no_data_group_bound_above_default")
			if !test.logged {
				if out.Len() != 0 {
					t.Fatalf("startup logged %q, want nothing", out.String())
				}
				return
			}
			for _, want := range []string{`"max_groups":100001`, `"default":100000`,
				"blocks Redis about 120-180 ms at 100,000 groups, 610 ms at 200,000 and 2.9 s at 1,000,000"} {
				if lines != 1 || !strings.Contains(out.String(), want) {
					t.Fatalf("startup logged %q, want one line carrying %s", out.String(), want)
				}
			}
		})
	}
}

// The line is written by the worker startup production runs, not only by the
// function: a worker opened with a raised bound logs it once.
func TestAWorkerOpenedWithARaisedNoDataGroupBoundLogsItsCost(t *testing.T) {
	address, _ := startPhaseTwoRedis(t)
	cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-group-bound")
	bound := 200000
	cfg.PhaseTwo.NoData.MaxGroups = &bound
	var out bytes.Buffer
	uq := &http.Client{Transport: controlledRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("no query is expected")
	})}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(context.Background(), cfg, metric.NewRecorder(metric.BuildInfo{}),
		observability.New(observability.ComponentRuntime, &out), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: uq,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	if lines := strings.Count(out.String(), "no_data_group_bound_above_default"); lines != 1 ||
		!strings.Contains(out.String(), `"max_groups":200000`) {
		t.Fatalf("worker startup logged %d bound lines in %q, want one carrying max_groups 200000", lines, out.String())
	}
}
