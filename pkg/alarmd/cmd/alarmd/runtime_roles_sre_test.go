// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/rollout"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/uq"
)

func TestSREProvidersAreRegisteredOnlyByTheChannelRole(t *testing.T) {
	address, _ := startPhaseTwoRedis(t)
	for _, selected := range []roles.Set{nil, {roles.Control, roles.Worker, roles.Channel}, {roles.Control, roles.Channel}, {roles.Channel}, {roles.Control}, {roles.Worker}} {
		t.Run(selected.String(), func(t *testing.T) {
			cfg := roleChannelAuthConfig(address)
			cfg.Roles = selected
			runtime, err := openProductionRoleChannel(cfg, ChannelBinding{})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			for _, operation := range []string{"pod.targets", "pod.get", "pod.logs", "pod.exec", "pod.delete", "workload.restart", "workload.scale", "secret.get", "configmap.get", "uq.egresses", "uq.query", "uq.trace"} {
				present := runtime.Executor.OperationContractRevision(operation) != ""
				if present != cfg.HasRole(roles.Channel) {
					t.Fatalf("%s registered in roles %v: %v", operation, selected, present)
				}
			}
		})
	}
}

func TestSREQueryReusesCurrentUQEndpointAndSource(t *testing.T) {
	var calls int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/prefix/query/ts/" || r.Header.Get("Bk-Query-Source") != config.DefaultQuerySource || r.Header.Get("X-Bk-Tenant-Id") != "tenant-fixture" || r.Header.Get("X-Bk-Scope-Space-Uid") != "scope-fixture" || r.Header.Get("X-Bkapi-Jwt") != "" {
			t.Errorf("SRE query diverged from the current native UQ connection: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"series":[]}`)
	}))
	defer backend.Close()
	cfg := config.Default()
	cfg.PhaseTwo.Access.UQEndpoint = backend.URL + "/prefix"
	cfg.PhaseTwo.Access.QuerySource = ""
	cfg.SRE.UQ.Egresses = []uq.Egress{{Name: "current", Tenants: []string{"tenant-fixture"}, Spaces: []string{"scope-fixture"}, Endpoints: []uq.Endpoint{{Path: "/query/ts/", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}}}}
	operations, closeProvider, err := channelSREOperations(cfg, ChannelBinding{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProvider()
	for _, operation := range operations {
		if operation.ID != "uq.query" {
			continue
		}
		out := operation.Run(context.Background(), obchannel.Params{"egress": "current", "endpoint": "/query/ts/", "tenant": "tenant-fixture", "space": "scope-fixture", "body": map[string]any{"start_time": "1700000000", "end_time": "1700000060", "query_list": []any{map[string]any{"table_id": "fixture.metric", "field_name": "value"}}}})
		if out.Error != nil || !out.Complete || calls != 1 {
			t.Fatalf("current runtime UQ was not used: %+v; calls=%d", out, calls)
		}
		return
	}
	t.Fatal("UQ query operation is missing")
}

// The audit sink writes one sre_action line per call, Info when it was done
// and Warn otherwise, with who, what and where, and counts it under its
// operation and result; every cell of the count exists once it is wired.
func TestTheSREAuditSinkWritesALineAndCountsTheCall(t *testing.T) {
	var out bytes.Buffer
	recorder := metric.NewRecorder(metric.BuildInfo{})
	sink := sreAuditSink(observability.New("channel", &out), recorder)
	sink(obchannel.SREAction{Op: "pod.delete", Namespace: "fixture", Kind: "Pod", Name: "web-0", UID: "pod-uid", Result: obchannel.SREResultTargetChanged, Code: "target_changed", SessionID: "session-1", RequestID: "request-1", DurationMS: 5})
	line := out.String()
	for _, part := range []string{`"stage":"sre_action"`, `"level":"WARN"`, `"op":"pod.delete"`, `"namespace":"fixture"`, `"session_id":"session-1"`, `"request_id":"request-1"`, `"result":"target_changed"`} {
		if !strings.Contains(line, part) {
			t.Fatalf("line lacks %s: %s", part, line)
		}
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	cells, counted := 0, 0.0
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_sre_actions_total" {
			continue
		}
		for _, series := range family.Metric {
			cells++
			labels := map[string]string{}
			for _, pair := range series.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			if labels["op"] == "pod.delete" && labels["result"] == "target_changed" {
				counted = series.GetCounter().GetValue()
			}
		}
	}
	if cells != len(obchannel.SREActionOps)*len(obchannel.SREActionResults) || counted != 1 {
		t.Fatalf("cells %d counted %v, want every cell and the call once", cells, counted)
	}
}

// An image change that ended writes one sre_rollout line - Warn unless it
// completed - with the receipt and both images, and counts under its final
// state; every final state has its cell once it is wired.
func TestTheRolloutSinkWritesALineAndCountsTheEnd(t *testing.T) {
	var out bytes.Buffer
	recorder := metric.NewRecorder(metric.BuildInfo{})
	sink := sreRolloutSink(observability.New("channel", &out), recorder)
	sink(rollout.Record{ID: "receipt-1", Namespace: "fixture", Name: "web", Container: "web", Previous: "web:v1", Image: "web:v2",
		State: rollout.StateReverted, SessionID: "session-1", StartedAt: 1000, EndedAt: 61000})
	line := out.String()
	for _, part := range []string{`"stage":"sre_rollout"`, `"level":"WARN"`, `"receipt_id":"receipt-1"`, `"previous_image":"web:v1"`, `"image":"web:v2"`, `"result":"reverted"`} {
		if !strings.Contains(line, part) {
			t.Fatalf("line lacks %s: %s", part, line)
		}
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	cells, counted := 0, 0.0
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_sre_rollouts_total" {
			continue
		}
		for _, series := range family.Metric {
			cells++
			if series.Label[0].GetValue() == rollout.StateReverted {
				counted = series.GetCounter().GetValue()
			}
		}
	}
	if cells != len(rollout.FinalStates) || counted != 1 {
		t.Fatalf("cells %d counted %v, want every final state and the end once", cells, counted)
	}
}

// The credentials the deployment holds are the redaction's literal secrets:
// the CLI administrator key, the link Console password, and every Redis
// connection's password and Sentinel password - the runtime store, each
// platform cache on a Redis of its own (strategy, CMDB, dynamic config,
// target groups) and the compatibility service Redis.
func TestTheRedactionKnowsEveryCredentialTheDeploymentHolds(t *testing.T) {
	cfg := config.Default()
	cfg.CLI.AdminKey = "fixture-admin-key"
	cfg.PhaseTwo.Linkd.Password = "fixture-linkd-password"
	cfg.Redis.Password = "fixture-redis-password"
	cfg.Redis.SentinelPassword = "fixture-sentinel-password"
	// Every platform cache on a Redis of its own, and the compatibility
	// service and pod cache Redis, each with its own credentials.
	own := func(name string) *config.RedisConnectionConfig {
		return &config.RedisConnectionConfig{Address: name + ".example.test:6379", Password: "fixture-" + name + "-password", SentinelPassword: "fixture-" + name + "-sentinel"}
	}
	cfg.PlatformCache.Strategy, cfg.PlatformCache.CMDB = own("strategy"), own("cmdb")
	cfg.PlatformCache.DynamicConfig, cfg.PlatformCache.TargetGroup = own("dynamic"), own("group")
	prefix := "fixture-groups"
	cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	cfg.Kafka.LegacyAdapter.ServiceRedis = *own("service")
	cfg.Kafka.LegacyAdapter.PodCache = &config.LegacyPodCacheConfig{Connection: *own("pods")}
	known := map[string]bool{}
	for _, value := range sreDeploymentSecrets(cfg) {
		known[value] = true
	}
	want := []string{"fixture-admin-key", "fixture-linkd-password", "fixture-redis-password", "fixture-sentinel-password"}
	for _, name := range []string{"strategy", "cmdb", "dynamic", "group", "service", "pods"} {
		want = append(want, "fixture-"+name+"-password", "fixture-"+name+"-sentinel")
	}
	for _, want := range want {
		if !known[want] {
			t.Fatalf("the redaction does not know %q: %v", want, known)
		}
	}
}
