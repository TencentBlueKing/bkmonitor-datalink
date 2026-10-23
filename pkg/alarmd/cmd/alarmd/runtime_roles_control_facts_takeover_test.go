// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/go-redis/redis/v8"
)

// controlFactsHealth is the part of /api/health this test reads, decoded
// loosely so the same assertions read any build's answer.
type controlFactsHealth struct {
	Health string `json:"health"`
	Gaps   []struct {
		Kind   string `json:"kind"`
		Detail string `json:"detail"`
	} `json:"gaps"`
	SourceReplica string `json:"source_replica"`
	ControlFacts  *struct {
		Source      string `json:"source"`
		Replica     string `json:"replica"`
		Unavailable string `json:"unavailable"`
	} `json:"control_facts"`
}

func readControlFactsHealth(t *testing.T, bundle *phaseTwoWorkerBundle) controlFactsHealth {
	t.Helper()
	recorder := httptest.NewRecorder()
	bundle.dependencies.FleetAPI.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/api/health = %d: %s", recorder.Code, recorder.Body.String())
	}
	var health controlFactsHealth
	if err := json.Unmarshal(recorder.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode /api/health: %v", err)
	}
	return health
}

func (health controlFactsHealth) hasGap(kind string) bool {
	for _, gap := range health.Gaps {
		if gap.Kind == kind {
			return true
		}
	}
	return false
}

// TestATakeoverKeepsTheFleetViewDecidedBeforeTheNewLeadersFirstWrite runs two
// replicas on the wiring startup uses -- the role runtime attached, no roles
// configured -- moves the control lease from one to the other, and reads the
// new Leader's fleet view before it has written its control record. The
// previous term's record (or a snapshot carrying the Leader's facts) stands
// in, so the view does not go UNKNOWN for want of control facts; after the
// first write the view reads the new term's record.
func TestATakeoverKeepsTheFleetViewDecidedBeforeTheNewLeadersFirstWrite(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()

	configFor := func(replica string) config.Config {
		cfg := validGoAccessRuntimeConfig()
		cfg.Redis.Address = address
		withCompatibilityOutput(&cfg, address)
		cfg.Redis.StatePrefix = "alarmd:phase-two:control-facts-takeover"
		cfg.PhaseTwo.Worker.ID = replica
		cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
		// The fleet publisher ticks on the reconcile interval. An hour keeps
		// it from ticking during the test, so the test decides when each
		// replica writes and can read the view before the new Leader's first
		// write.
		cfg.PhaseTwo.Control.ReconcileInterval = config.Duration(time.Hour)
		cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
		return cfg
	}
	recorders := map[string]*metric.Recorder{}
	open := func(cfg config.Config) *phaseTwoWorkerBundle {
		runtime := openRoleRuntimeForTest(t, cfg)
		recorder := metric.NewRecorder(metric.BuildInfo{})
		recorders[cfg.PhaseTwo.Worker.ID] = recorder
		bundle, openErr := openProductionPhaseTwoBundleWithDependencies(
			ctx, cfg, recorder, observability.Discard(observability.ComponentRuntime),
			newPhaseTwoApplicationHealth(),
			func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
				return controlplane.NewLegacyRedisStrategySource(client, prefix)
			},
			phaseTwoProductionExternalDependencies{
				RoleRuntime: runtime, Now: time.Now, HTTPClient: uqServer.Client(),
				PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
					return &recordingPhaseTwoEventSink{}, nil
				}),
			},
		)
		if openErr != nil {
			t.Fatalf("open production bundle %s: %v", cfg.PhaseTwo.Worker.ID, openErr)
		}
		return bundle
	}

	first := open(configFor("takeover-replica-a"))
	if err := first.Start(ctx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if stats := first.dependencies.ViewStreamStats(); !stats.Leading || stats.ControlEpoch == 0 {
		t.Fatalf("first replica stream = %+v, want it leading", stats)
	}
	firstEpoch := first.dependencies.ViewStreamStats().ControlEpoch
	first.dependencies.PublishFleet(ctx)
	if health := readControlFactsHealth(t, first); health.hasGap("CONTROL_FACTS_UNAVAILABLE") {
		t.Fatalf("first Leader's own view = %+v", health)
	}

	second := open(configFor("takeover-replica-b"))
	t.Cleanup(func() {
		if err := second.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := second.Start(ctx); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if second.dependencies.ViewStreamStats().Leading {
		t.Fatal("second replica leads while the first holds the control lease")
	}
	second.dependencies.PublishFleet(ctx)

	// The takeover: the first Leader stops and releases its lease, and the
	// second acquires it on its next reconcile round.
	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown() error = %v", err)
	}
	waitPhaseTwoCondition(t, 10*time.Second, "second replica takes the control lease", func() bool {
		if err := second.refreshAndReconcile(ctx, true); err != nil {
			return false
		}
		stats := second.dependencies.ViewStreamStats()
		return stats.Leading && stats.ControlEpoch > firstEpoch
	})

	before := readControlFactsHealth(t, second)
	t.Logf("before the new Leader's first write: health %s, control facts %+v, gaps %+v", before.Health, before.ControlFacts, before.Gaps)
	if before.hasGap("CONTROL_FACTS_UNAVAILABLE") {
		t.Fatalf("view before the new Leader's first write has the control facts gap: %+v", before)
	}
	if before.Health == "UNKNOWN" && len(before.Gaps) == 0 {
		t.Fatalf("view before the new Leader's first write is UNKNOWN without a named gap: %+v", before)
	}
	if before.ControlFacts == nil || (before.ControlFacts.Source != "previous_term" && before.ControlFacts.Source != "embedded") ||
		before.ControlFacts.Unavailable == "" {
		t.Fatalf("control facts before the new Leader's first write = %+v, want previous_term or embedded with the reason", before.ControlFacts)
	}

	second.dependencies.PublishFleet(ctx)
	after := readControlFactsHealth(t, second)
	if after.hasGap("CONTROL_FACTS_UNAVAILABLE") || after.ControlFacts == nil || after.ControlFacts.Source != "record" ||
		after.ControlFacts.Replica != "takeover-replica-b" || after.ControlFacts.Unavailable != "" {
		t.Fatalf("control facts after the new Leader's first write = %+v (gaps %+v)", after.ControlFacts, after.Gaps)
	}
	if written := counterValue(t, recorders["takeover-replica-b"], "bkmonitor_alarmd_fleet_control_facts_writes_total",
		map[string]string{"result": "written"}); written != 1 {
		t.Fatalf("new Leader's control record writes = %v, want the one write counted", written)
	}
}
