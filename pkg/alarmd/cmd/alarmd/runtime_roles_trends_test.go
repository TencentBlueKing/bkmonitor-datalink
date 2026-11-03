// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// The process that hosts the channel answers trends.get from the handler it
// binds to the channel, in either topology: a split control process (control
// and channel roles, the workers elsewhere) as well as one process running
// every role. Before, the split control process bound a handler without
// trends, and /api/series there was a 404.
//
// Out of a cluster the DataID read says so, which is how the answer shows
// the trends were wired with the in-cluster space derivation.
func TestTheProcessHostingTheChannelServesTrendsInEitherTopology(t *testing.T) {
	for _, topology := range []struct {
		name  string
		roles roles.Set
	}{
		{"split control", roles.Set{roles.Control, roles.Channel}},
		{"every role in one process", nil},
	} {
		t.Run(topology.name, func(t *testing.T) {
			address, _ := startPhaseTwoRedis(t)
			uq := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write([]byte(`{"series":[]}`))
			}))
			t.Cleanup(uq.Close)
			cfg := validGoAccessRuntimeConfig()
			if topology.roles != nil {
				cfg.Roles = topology.roles
			}
			cfg.Redis.Address = address
			cfg.Redis.StatePrefix = "trends-topology-test"
			cfg.PhaseTwo.Worker.ID = "trends-1"
			cfg.PhaseTwo.Access.UQEndpoint = uq.URL
			cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Hour)
			withCompatibilityOutput(&cfg, address)
			runtime := openRoleRuntimeForTest(t, cfg)
			var bound ChannelBinding
			bind := runtime.Channel.Bind
			runtime.Channel.Bind = func(binding ChannelBinding) {
				bound = binding
				bind(binding)
			}
			bundle, err := openProductionPhaseTwoBundleWithDependencies(context.Background(), cfg,
				metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
				func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
					return controlplane.NewLegacyRedisStrategySource(client, prefix)
				},
				phaseTwoProductionExternalDependencies{RoleRuntime: runtime, Now: time.Now, HTTPClient: uq.Client(),
					PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
						return &recordingPhaseTwoEventSink{}, nil
					})})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := bundle.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			})
			if bound.Native == nil {
				t.Fatal("the bundle bound no handler to the channel")
			}
			response := httptest.NewRecorder()
			bound.Native.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/series?window=1h", nil))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "not in a cluster") {
				t.Fatalf("/api/series through the channel's handler = %d %s, want the trends with the DataID read's own refusal",
					response.Code, response.Body.String())
			}
		})
	}
}
