// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/publicsurface"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestRoleChannelConstructsWithoutBusinessIO(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var dials atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			_ = connection.Close()
		}
	}()
	cfg := config.Default()
	cfg.Roles = roles.Set{roles.Channel}
	cfg.Redis.Address = listener.Addr().String()
	cfg.PhaseTwo.Worker.ID = "channel"
	cfg.CLI = config.CLIConfig{EnvironmentID: "fixture", EnvironmentName: "Fixture", PublicBaseURL: "http://ob.example/alarmd/", AdminKey: strings.Repeat("x", 32)}
	runtime, err := openProductionRoleChannel(cfg, ChannelBinding{Control: cliControlBinding{Incarnation: "boot", StreamToken: "internal"}})
	if err == nil {
		runtime.Bind(ChannelBinding{Native: http.NotFoundHandler()})
	}
	_ = listener.Close()
	<-done
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if dials.Load() != 0 || runtime.Executor == nil || runtime.EvidenceHandler == nil || !runtime.HTTPConfigured {
		t.Fatalf("role construction touched business dependencies or missed provider: dials=%d", dials.Load())
	}
	_, err = runtime.EvidenceHandler(context.Background(), &pb.EvidenceRequest{EnvironmentId: "other"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("provider missing without HTTP auth: %v", err)
	}
	response := roleRuntimeInvoke(runtime, "lookback.get")
	if response.Error == nil || response.Error.Code != "operation_unavailable" {
		t.Fatalf("channel answered fake Worker lookback: %+v", response)
	}
}

func roleChannelAuthConfig(address string) config.Config {
	cfg := config.Default()
	cfg.Roles = roles.Set{roles.Channel}
	cfg.Redis.Address = address
	cfg.PhaseTwo.Worker.ID = "channel"
	cfg.PhaseTwo.Control.StrategyCachePrefix = "alarm-config"
	cfg.CLI = config.CLIConfig{EnvironmentID: "fixture", EnvironmentName: "Fixture", PublicBaseURL: "http://ob.example/alarmd/", AdminKey: strings.Repeat("x", 32)}
	return cfg
}

func TestEarlyChannelBoundRedisCallbacksReportAndSurviveLaterBindings(t *testing.T) {
	cfg := roleChannelAuthConfig("127.0.0.1:1")
	runtime, err := openProductionRoleChannel(cfg, ChannelBinding{Control: cliControlBinding{Incarnation: "boot"}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	var mu sync.Mutex
	failures, retries := map[string]int{}, map[string]int{}
	var badFailure bool
	runtime.Bind(ChannelBinding{Control: cliControlBinding{
		RedisFailures: func(client, reason, detail string) {
			mu.Lock()
			defer mu.Unlock()
			failures[client]++
			badFailure = badFailure || reason == "" || client == "auth" && detail == "" || client == "evidence" && detail != ""
		},
		RedisDialRetries: func(client, reason string) {
			mu.Lock()
			defer mu.Unlock()
			retries[client]++
			badFailure = badFailure || reason == ""
		},
	}})
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt == 2 {
			// Later business bindings may omit telemetry; the last installed
			// callbacks still belong to the already-created lazy pools.
			runtime.Bind(ChannelBinding{Native: http.NotFoundHandler()})
		}
		response := runtime.Executor.ExecuteEvidence(context.Background(), obchannel.Invocation{EnvironmentID: "fixture", Version: obchannel.Version,
			OperationContractRevision: runtime.Executor.OperationContractRevision("store.inspect"), Operation: "store.inspect", RequestID: "fixture-read",
			Params: obchannel.Params{"family": "source_strategy", "strategy_id": "7"}, Target: obchannel.Target{Replica: "channel", ExpectedIncarnation: "boot"}})
		if response.Evidence.Complete || response.Error == nil || response.Error.Code != "evidence_unavailable" {
			t.Fatalf("unreachable evidence store did not execute its read contract: %+v", response)
		}
		if err := runtime.Ready(context.Background()); cliauth.ErrorCode(err) != "auth_store_unavailable" {
			t.Fatalf("unreachable auth readiness: %v", err)
		}
		mu.Lock()
		bad := badFailure || failures["evidence"] < attempt || failures["auth"] < attempt || retries["evidence"] < attempt || retries["auth"] < attempt
		mu.Unlock()
		if bad {
			t.Fatalf("attempt %d lost bound Redis callbacks: failures=%v retries=%v", attempt, failures, retries)
		}
	}
}

func TestRoleChannelReadyUsesAuthorizationLuaAndRecovers(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	cfg := roleChannelAuthConfig(address)
	runtime, err := openProductionRoleChannel(cfg, ChannelBinding{Control: cliControlBinding{Incarnation: "boot"}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ctx := context.Background()
	revision := runtime.Executor.CatalogRevision()
	pairingsKey := fmt.Sprintf("%s.cli:{%x}:pairings", cfg.Redis.StatePrefix, sha256.Sum256([]byte(cfg.CLI.EnvironmentID)))
	now, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(ctx, pairingsKey,
		&redis.Z{Score: float64(now.Add(-cliauth.PairingIdleLifetime - time.Minute).UnixMilli()), Member: "expired"},
		&redis.Z{Score: float64(now.UnixMilli()), Member: "active"}).Err(); err != nil {
		t.Fatal(err)
	}
	if !runtime.HTTPConfigured || runtime.Ready(ctx) != nil {
		t.Fatal("healthy auth store failed its read contract")
	}
	if pairings, err := client.ZRange(ctx, pairingsKey, 0, -1).Result(); err != nil || len(pairings) != 1 || pairings[0] != "active" {
		t.Fatalf("readiness changed pairing housekeeping: %v %v", pairings, err)
	}
	if err := client.Del(ctx, pairingsKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, pairingsKey, "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ready(ctx); cliauth.ErrorCode(err) != "auth_store_unavailable" {
		t.Fatalf("readiness accepted a reachable store with a broken auth Lua contract: %v", err)
	}
	if err := client.Del(ctx, pairingsKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ready(ctx); err != nil {
		t.Fatalf("readiness did not recover after auth contract repaired: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	start := time.Now()
	if err := runtime.Ready(cancelled); err == nil || time.Since(start) > time.Second {
		t.Fatalf("readiness ignored caller cancellation: %v", err)
	}
	if runtime.Executor.CatalogRevision() != revision {
		t.Fatal("readiness changed the operation schema")
	}
}

func TestRoleChannelReadyNamesDisabledHTTPAuth(t *testing.T) {
	for _, name := range []string{"no-key", "worker", "control", "invalid-auth"} {
		t.Run(name, func(t *testing.T) {
			cfg := roleChannelAuthConfig("127.0.0.1:1")
			switch name {
			case "no-key":
				cfg.CLI.AdminKey = ""
			case "worker":
				cfg.Roles = roles.Set{roles.Worker}
			case "control":
				cfg.Roles = roles.Set{roles.Control}
			case "invalid-auth":
				cfg.CLI.PublicBaseURL = "invalid"
			}
			var failures atomic.Int32
			runtime, err := openProductionRoleChannel(cfg, ChannelBinding{Control: cliControlBinding{Incarnation: "boot", RedisFailures: func(string, string, string) { failures.Add(1) }}})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			if runtime.HTTPConfigured || cliauth.ErrorCode(runtime.Ready(context.Background())) != "channel_disabled" || failures.Load() != 0 {
				t.Fatal("disabled readiness touched auth storage or omitted its named reason")
			}
		})
	}
}

func TestExplicitBusinessRolesKeepPublicSurfaceRestrictedWithoutHTTPChannel(t *testing.T) {
	for _, role := range []roles.Role{roles.Worker, roles.Control} {
		for _, key := range []string{strings.Repeat("x", 32), ""} {
			t.Run(fmt.Sprintf("%s/key=%t", role, key != ""), func(t *testing.T) {
				cfg := roleChannelAuthConfig("127.0.0.1:1")
				cfg.Roles = roles.Set{role}
				cfg.CLI.AdminKey = key
				runtime, err := openProductionRoleChannel(cfg, ChannelBinding{Native: standInAPI(), Control: cliControlBinding{
					Incarnation: "boot", StreamToken: "internal", PublicWindows: windowsStandIn}})
				if err != nil {
					t.Fatal(err)
				}
				defer runtime.Close()
				if !runtime.Restricted || runtime.HTTPConfigured || runtime.EvidenceHandler == nil {
					t.Fatal("explicit business role exposed its native surface or omitted internal evidence")
				}
				for _, path := range []string{"/api/objects", "/api/diagnose", "/api/k8s/pods", "/api/store", "/api/evidence", "/api/deployment"} {
					got := publicCall(runtime.Handler, http.MethodGet, path)
					if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), publicsurface.RestrictedCode) || strings.Contains(got.Body.String(), plantedAddress) || strings.Contains(got.Body.String(), "deployment\":") {
						t.Fatalf("public business route %s leaked evidence: %d %s", path, got.Code, got.Body.String())
					}
				}
				var health fleet.PublicHealthResponse
				got := publicCall(runtime.Handler, http.MethodGet, "/api/health")
				if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &health) != nil || !health.Restricted || strings.Contains(got.Body.String(), plantedAddress) {
					t.Fatalf("public health summary leaked native details: %d %s", got.Code, got.Body.String())
				}
				if got := publicCall(runtime.Handler, http.MethodGet, "/api/windows"); got.Code != 299 {
					t.Fatalf("public windows missing: %d", got.Code)
				}
				for _, path := range []string{"/api/cli/channel", "/api/cli/auth/grants"} {
					if got := publicCall(runtime.Handler, http.MethodPost, path); got.Code != http.StatusServiceUnavailable || !strings.Contains(got.Body.String(), "cli_not_configured") {
						t.Fatalf("business role exposed an HTTP CLI endpoint: %d %s", got.Code, got.Body.String())
					}
				}
				if _, err := runtime.EvidenceHandler(context.Background(), &pb.EvidenceRequest{EnvironmentId: "other"}); status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("public restriction disabled the internal provider: %v", err)
				}
			})
		}
	}
}

func roleRuntimeInvoke(runtime *roleChannelRuntime, operation string) obchannel.Response {
	return runtime.Executor.ExecuteEvidence(context.Background(), obchannel.Invocation{EnvironmentID: "fixture", Version: obchannel.Version,
		OperationContractRevision: runtime.Executor.OperationContractRevision(operation), Operation: operation, RequestID: "fixture-read",
		Params: obchannel.Params{}, Target: obchannel.Target{Replica: "channel", ExpectedIncarnation: "boot"}})
}

func TestRoleEvidenceBindingKeepsIdentityAndUpdatesProviders(t *testing.T) {
	cfg := config.Default()
	cfg.Roles = roles.Set{roles.Worker}
	cfg.PhaseTwo.Worker.ID = "channel"
	runtime, err := openProductionRoleChannel(cfg, ChannelBinding{EnvironmentID: "fixture", Control: cliControlBinding{Incarnation: "boot", StreamToken: "internal"}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if response := roleRuntimeInvoke(runtime, "lookback.get"); response.Error == nil || response.Error.Code != "business_waiting" {
		t.Fatalf("waiting Worker fabricated lookback: %+v", response)
	}
	revision := runtime.Executor.CatalogRevision()
	runtime.Bind(ChannelBinding{InstanceID: "different", EnvironmentID: "different", Roles: roles.Set{roles.Channel},
		Native: http.NotFoundHandler(), Facts: func() *observability.RuntimeConfigFacts { return &observability.RuntimeConfigFacts{Digest: "bound"} }})
	response := roleRuntimeInvoke(runtime, "runtime.get")
	reading, ok := response.Result.(cliRuntimeFacts)
	if !ok || reading.Config == nil || reading.Config.Digest != "bound" || response.Meta.AnsweredBy != "channel" || response.Meta.EnvironmentID != "fixture" || response.Meta.Incarnation != "boot" || !response.Meta.Roles.Has(roles.Worker) {
		t.Fatalf("binding changed identity or failed to update process facts: %+v", response)
	}
	if runtime.Executor.CatalogRevision() != revision {
		t.Fatal("binding changed the operation contract")
	}
	if response := roleRuntimeInvoke(runtime, "lookback.get"); response.Error != nil || !response.Evidence.Complete {
		t.Fatalf("bound disabled lookback rejected: %+v", response)
	}
}

func TestEarlyChannelBindsEvidenceAfterBusinessServerBecomesAvailable(t *testing.T) {
	cfg := config.Default()
	cfg.PhaseTwo.Worker.ID = "channel"
	runtime, err := openProductionRoleChannel(cfg, ChannelBinding{EnvironmentID: "fixture", Control: cliControlBinding{Incarnation: "boot", StreamToken: "internal"}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server, err := viewstream.NewServer(viewStreamAdmission{}, observability.NopObserver{}, viewstream.ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runtime.Bind(ChannelBinding{Native: http.NotFoundHandler(), Control: cliControlBinding{Server: server}})
	_, err = server.ReadEvidence(context.Background(), &pb.EvidenceRequest{EnvironmentId: "other"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("business Server lost the early evidence handler: %v", err)
	}
}

func TestSplitChannelReadsAuthlessWorkerThroughRegisteredGRPC(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	cfg := roleChannelAuthConfig(address)
	// The existing browser grant helper uses this deployment origin.
	cfg.CLI.PublicBaseURL = "https://ob.example/alarmd/"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := ownership.NewRedisStoreWithClient(client, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := viewstream.NewServer(viewStreamAdmission{}, observability.NopObserver{}, viewstream.ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	workerCfg := cfg
	workerCfg.Roles = roles.Set{roles.Worker}
	workerCfg.PhaseTwo.Worker.ID, workerCfg.CLI.AdminKey = "worker", ""
	var nativeReads atomic.Int32
	worker, err := openProductionRoleChannel(workerCfg, ChannelBinding{
		Native: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nativeReads.Add(1); standInAPI().ServeHTTP(w, r) }),
		Facts: func() *observability.RuntimeConfigFacts {
			return &observability.RuntimeConfigFacts{Digest: "worker-facts"}
		},
		Control: cliControlBinding{Server: server, Incarnation: "worker-boot", StreamToken: "worker-internal-fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	entry, err := openProductionRoleChannel(cfg, ChannelBinding{Control: cliControlBinding{Incarnation: "channel-boot", StreamToken: "channel-internal-fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	if !entry.HTTPConfigured || worker.HTTPConfigured || worker.EvidenceHandler == nil || cliauth.ErrorCode(worker.Ready(ctx)) != "channel_disabled" {
		t.Fatal("split fixture did not separate HTTP authorization from the Worker evidence provider")
	}
	if got := publicCall(worker.Handler, http.MethodPost, "/api/cli/channel"); got.Code != http.StatusServiceUnavailable || !strings.Contains(got.Body.String(), "cli_not_configured") {
		t.Fatalf("authless Worker exposed an HTTP channel: %d %s", got.Code, got.Body.String())
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan *pb.EvidenceRequest, 16)
	var rpcReads atomic.Int32
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if evidence, ok := req.(*pb.EvidenceRequest); ok {
			rpcReads.Add(1)
			requests <- proto.Clone(evidence).(*pb.EvidenceRequest)
		}
		return handler(ctx, req)
	}))
	pb.RegisterControlServiceServer(grpcServer, server)
	go grpcServer.Serve(listener)
	defer grpcServer.Stop()
	entryIdentity := ownership.InstanceRegistration{InstanceID: "channel", Roles: roles.Set{roles.Channel}, Incarnation: "channel-boot", InternalToken: "channel-internal-fixture", ExpiresAt: time.Now().Add(time.Minute)}
	workerIdentity := ownership.InstanceRegistration{InstanceID: "worker", Roles: roles.Set{roles.Worker}, Endpoint: listener.Addr().String(), Incarnation: "worker-boot", InternalToken: "worker-internal-fixture", ExpiresAt: time.Now().Add(time.Minute)}
	register := func(identity ownership.InstanceRegistration) {
		t.Helper()
		if err := store.RegisterInstance(ctx, identity); err != nil {
			t.Fatal(err)
		}
	}
	register(entryIdentity)
	register(workerIdentity)
	if _, found, err := store.ReadActiveControlLeader(ctx); err != nil || found {
		t.Fatalf("split fixture unexpectedly depends on a control Leader: found=%v err=%v", found, err)
	}
	session := openCLISession(t, entry.Handler, cfg)
	revision := cliDiscover(t, entry.Handler, session.AccessToken)
	invoke := func(token, operation string, params obchannel.Params) obchannel.Response {
		return cliCall(t, entry.Handler, token, map[string]any{"channel_version": obchannel.Version, "mode": "invoke", "operation": operation,
			"expected_catalog_revision": revision, "params": params})
	}
	for _, operation := range []string{"fleet.get", "runtime.get"} {
		out := invoke(session.AccessToken, operation, obchannel.Params{"replica": "worker", "expected_incarnation": "worker-boot"})
		raw, _ := json.Marshal(out.Result)
		wantStatus := "ok"
		if operation == "runtime.get" {
			// This fixture omits PlatformSettings; runtime.get requires both
			// Config and PlatformSettings for a complete response.
			wantStatus = "partial"
		}
		if out.Status != wantStatus || out.Meta.AnsweredBy != "worker" || out.Meta.Incarnation != "worker-boot" || !out.Meta.Roles.Has(roles.Worker) || out.Meta.Roles.Has(roles.Channel) ||
			out.Meta.EnvironmentID != cfg.CLI.EnvironmentID || out.Meta.Revision != revision || out.Meta.Session == nil || strings.Join(out.Meta.Via, ",") != "channel" {
			t.Fatalf("%s lost split target/session metadata: %+v", operation, out)
		}
		want := plantedAddress
		if operation == "runtime.get" {
			want = "worker-facts"
		}
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%s answered entry facts instead of Worker facts: %s", operation, raw)
		}
		request := <-requests
		if request.Phase != "direct" || request.WorkerId != "channel" || request.TargetWorkerId != "worker" || request.CallerIncarnation != "channel-boot" ||
			request.StreamToken != entryIdentity.InternalToken || request.StreamToken == session.AccessToken || request.StreamToken == cfg.CLI.AdminKey || request.OperationContractRevision != worker.Executor.OperationContractRevision(operation) {
			t.Fatalf("HTTP authorization did not stay at the ingress: %+v", request)
		}
	}
	if nativeReads.Load() != 1 || rpcReads.Load() != 2 {
		t.Fatalf("legacy reads did not execute through actual Worker RPC: native=%d rpc=%d", nativeReads.Load(), rpcReads.Load())
	}
	for _, rejection := range []struct {
		name, token, code string
		params            obchannel.Params
		change            func()
	}{
		{name: "invalid_session", token: "invalid-session", code: "auth_expired_or_revoked", params: obchannel.Params{"replica": "worker"}},
		{name: "missing_target", token: session.AccessToken, code: "target_unavailable", params: obchannel.Params{"replica": "absent"}},
		{name: "pinned_old_incarnation", token: session.AccessToken, code: "target_changed", params: obchannel.Params{"replica": "worker", "expected_incarnation": "old-worker-boot"}},
		{name: "restarted_target", token: session.AccessToken, code: "target_changed", params: obchannel.Params{"replica": "worker"}, change: func() { changed := workerIdentity; changed.Incarnation = "new-worker-boot"; register(changed) }},
		{name: "restarted_caller", token: session.AccessToken, code: "worker_identity_rejected", params: obchannel.Params{"replica": "worker"}, change: func() { changed := entryIdentity; changed.Incarnation = "new-channel-boot"; register(changed) }},
	} {
		t.Run(rejection.name, func(t *testing.T) {
			if rejection.change != nil {
				rejection.change()
				defer register(entryIdentity)
				defer register(workerIdentity)
			}
			reads := nativeReads.Load()
			out := invoke(rejection.token, "fleet.get", rejection.params)
			if out.Error == nil || out.Error.Code != rejection.code || nativeReads.Load() != reads {
				t.Fatalf("rejected target or credential executed Worker evidence: %+v", out)
			}
		})
	}
	connection, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	internalClient := pb.NewControlServiceClient(connection)
	internalRead := func(change func(*pb.EvidenceRequest)) (obchannel.Response, error) {
		req := &pb.EvidenceRequest{Phase: "direct", WorkerId: entryIdentity.InstanceID, StreamToken: entryIdentity.InternalToken, CallerIncarnation: entryIdentity.Incarnation,
			EnvironmentId: cfg.CLI.EnvironmentID, RequestId: "split-internal-read", ChannelVersion: obchannel.Version,
			CatalogRevision: revision, Operation: "fleet.get", OperationContractRevision: worker.Executor.OperationContractRevision("fleet.get"), ParamsJson: []byte(`{}`),
			TargetWorkerId: workerIdentity.InstanceID, ExpectedIncarnation: workerIdentity.Incarnation}
		change(req)
		result, err := internalClient.ReadEvidence(ctx, req)
		if err != nil {
			return obchannel.Response{}, err
		}
		var out obchannel.Response
		if err := json.Unmarshal(result.ResponseJson, &out); err != nil || out.Meta.Session != nil {
			t.Fatalf("internal evidence carried an invalid response or CLI session: %v %+v", err, out)
		}
		return out, nil
	}
	reads := nativeReads.Load()
	if _, err := internalRead(func(req *pb.EvidenceRequest) { req.StreamToken = session.AccessToken }); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("CLI session authorized the internal RPC: %v", err)
	}
	changedCaller := entryIdentity
	changedCaller.Roles = roles.Set{roles.Worker}
	register(changedCaller)
	if _, err := internalRead(func(*pb.EvidenceRequest) {}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("non-channel caller role authorized direct RPC: %v", err)
	}
	register(entryIdentity)
	if out, err := internalRead(func(req *pb.EvidenceRequest) { req.Operation = "unregistered.get" }); err != nil || out.Error == nil || out.Error.Code != "operation_not_allowed" {
		t.Fatalf("unregistered internal operation escaped the allowlist: %v %+v", err, out)
	}
	if nativeReads.Load() != reads {
		t.Fatal("internal permission rejection executed the Worker provider")
	}
}
