// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package blackbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evidenceroute"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// The routing store is a constructed lease/registry fixture, not a replacement
// for ownership's real-Redis lease tests. Authentication and evidence use Redis.
type routingFixtureStore struct {
	workers       map[string]ownership.WorkerRegistration
	leader, owner ownership.QueryGroupOwner
	reads         atomic.Int32
}

func (s *routingFixtureStore) ReadWorker(_ context.Context, id string) (ownership.WorkerRegistration, bool, error) {
	s.reads.Add(1)
	w, ok := s.workers[id]
	return w, ok, nil
}
func (s *routingFixtureStore) ReadActiveControlLeader(context.Context) (ownership.QueryGroupOwner, bool, error) {
	s.reads.Add(1)
	return s.leader, true, nil
}
func (s *routingFixtureStore) ReadQueryGroupOwner(_ context.Context, id execution.QueryGroupIdentity) (ownership.QueryGroupOwner, bool, error) {
	s.reads.Add(1)
	return s.owner, string(id) == "fixture-qg", nil
}

type routingAuth struct {
	*cliauth.Manager
	admissions, renewals atomic.Int32
}

func (a *routingAuth) Admit(ctx context.Context, session cliauth.Session, renew bool) (cliauth.Session, error) {
	a.admissions.Add(1)
	s, err := a.Manager.Admit(ctx, session, renew)
	if err == nil && s.Renewed {
		a.renewals.Add(1)
	}
	return s, err
}

type routingAdmission struct{}

func (routingAdmission) Admit(context.Context, string, string) (string, error) { return "", nil }

type routingNode struct {
	channel        atomic.Pointer[obchannel.Channel]
	router         *evidenceroute.Router
	auth           *routingAuth
	runs, rpcCalls atomic.Int32
}

type rpcReceipt struct {
	Receiver string `json:"receiver"`
	Caller   string `json:"caller"`
	Phase    string `json:"phase"`
	Target   string `json:"target"`
}

func TestActualCLIMultiInstanceControlRPC(t *testing.T) {
	h := newHarness(t, "private_ca")
	defer h.report()
	now := time.Now().UTC()
	store := &routingFixtureStore{workers: map[string]ownership.WorkerRegistration{},
		leader: ownership.QueryGroupOwner{OwnerID: "leader", OwnerEpoch: 7, Deadline: now.Add(time.Hour), ObservedAt: now},
		owner:  ownership.QueryGroupOwner{OwnerID: "worker", OwnerEpoch: 19, Deadline: now.Add(time.Hour), ObservedAt: now}}
	nodes := map[string]*routingNode{}
	var forbidden atomic.Value
	forbidden.Store([]string{})
	var receiptMu sync.Mutex
	var receipts []rpcReceipt
	defer func() {
		receiptMu.Lock()
		defer receiptMu.Unlock()
		report := map[string]any{"fixture_boundary": "Registry and active leases are constructed data; production Redis lease behavior is covered by alarmd ownership tests.", "rpc_receipts": receipts,
			"server_packages": []string{"cliauth", "obchannel", "obevidence", "evidenceroute", "viewstream"}, "routing_store_reads": store.reads.Load()}
		for id, node := range nodes {
			report[id] = map[string]int32{"runtime_runs": node.runs.Load(), "rpc_calls": node.rpcCalls.Load(), "session_admissions": node.auth.admissions.Load(), "session_renewals": node.auth.renewals.Load()}
		}
		raw, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(filepath.Join(h.dir, "routing-report.json"), append(raw, '\n'), 0600); err != nil {
			t.Error("cannot save routing report")
		}
	}()

	// Distinct source cache content proves that a targeted store read executes
	// with the worker's configured Redis binding, not the ingress's binding.
	seed, err := h.redis.Get(context.Background(), "fixture.strategy_7").Bytes()
	if err != nil {
		t.Fatal("cannot read constructed source fixture")
	}
	seed = bytes.ReplaceAll(seed, []byte(`"threshold":80`), []byte(`"threshold":91`))
	if err := h.redis.Set(context.Background(), "worker-fixture.strategy_7", seed, time.Minute).Err(); err != nil {
		t.Fatal("cannot seed worker source")
	}

	makeChannel := func(id, incarnation string, changedCatalog bool) *obchannel.Channel {
		node := nodes[id]
		prefix := "fixture"
		if id == "worker" {
			prefix = "worker-fixture"
		}
		service := obevidence.New(obevidence.Options{SourceStrategy: obevidence.RedisBinding{Client: h.redis, Location: obevidence.Location{Role: "strategy_cache", Address: h.redis.Options().Addr, Mode: "single", Prefix: prefix}}})
		operations := append(obchannel.NativeOperations(http.HandlerFunc(nativeFixture)), obchannel.StoreOperations(service)...)
		fields := map[string]obchannel.Field{}
		if changedCatalog {
			fields["new_optional_field"] = obchannel.Field{Type: "string", Description: "Constructed mixed-version contract."}
		}
		operations = append(operations, obchannel.Operation{ID: "runtime.get", Summary: "Read constructed process facts.", EvidenceScope: "process", Targetable: true, Fields: fields,
			Run: func(context.Context, obchannel.Params) obchannel.Outcome {
				node.runs.Add(1)
				return obchannel.Outcome{Complete: true, Value: map[string]any{"process": id, "incarnation": incarnation}, Next: []obchannel.Call{{Operation: "runtime.get", Params: obchannel.Params{}, Reason: "Repeat against this process."}}}
			}})
		channel, err := obchannel.New(obchannel.Options{Auth: node.auth, EnvironmentID: environment, Replica: id, Build: id + "-build", Incarnation: incarnation, Operations: operations,
			Route: func(ctx context.Context, call obchannel.Invocation) obchannel.Response {
				return node.router.Invoke(ctx, call)
			}})
		if err != nil {
			t.Fatal("cannot initialize routed channel")
		}
		return channel
	}
	for _, id := range []string{"entry", "leader", "worker"} {
		node := &routingNode{auth: &routingAuth{Manager: h.manager}}
		nodes[id] = node
		node.channel.Store(makeChannel(id, id+"-boot-1", false))
		streamToken := "constructed-" + id + "-stream-credential"
		h.secrets = append(h.secrets, streamToken)
		node.router, err = evidenceroute.New(evidenceroute.Options{Store: store, WorkerID: id, StreamToken: streamToken, EnvironmentID: environment, Build: id + "-build", Incarnation: id + "-boot-1", CatalogRevision: node.channel.Load().CatalogRevision(),
			Execute: func(ctx context.Context, call obchannel.Invocation) obchannel.Response {
				return node.channel.Load().ExecuteEvidence(ctx, call)
			}})
		if err != nil {
			t.Fatal("cannot initialize control router")
		}
		control, err := viewstream.NewServer(routingAdmission{}, observability.NopObserver{}, viewstream.ServerOptions{})
		if err != nil {
			t.Fatal("cannot initialize real control service")
		}
		control.SetEvidenceHandler(func(ctx context.Context, request *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
			node.rpcCalls.Add(1)
			raw, _ := proto.Marshal(request)
			for _, secret := range forbidden.Load().([]string) {
				if secret != "" && bytes.Contains(raw, []byte(secret)) {
					t.Error("CLI or admin credential entered ControlService RPC")
				}
			}
			receiptMu.Lock()
			receipts = append(receipts, rpcReceipt{Receiver: id, Caller: request.WorkerId, Phase: request.Phase, Target: request.TargetWorkerId})
			receiptMu.Unlock()
			return node.router.Handle(ctx, request)
		})
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal("cannot listen for fixture control RPC")
		}
		server := grpc.NewServer()
		pb.RegisterControlServiceServer(server, control)
		go server.Serve(listener)
		t.Cleanup(server.Stop)
		store.workers[id] = ownership.WorkerRegistration{WorkerID: id, StreamToken: streamToken, Endpoint: listener.Addr().String(), ExpiresAt: now.Add(time.Hour)}
	}
	// No requests have started. Reuse all original direct admin-key handling and only
	// replace the channel endpoint; this avoids duplicating the login harness.
	original := h.server.Config.Handler
	h.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alarmd/api/cli/channel" {
			original.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/api/cli/channel"
		h.requestMu.Lock()
		h.requests = append(h.requests, r.Method+" "+r.URL.Path)
		h.requestMu.Unlock()
		nodes["entry"].channel.Load().ServeHTTP(w, r)
	})

	grant := h.grant()
	h.run("multi-login", 0, grant+"\n", "auth", "login", "--ca-cert", h.caFile)
	token := h.profileToken()
	digest := sha256.Sum256([]byte(token))
	h.secrets = append(h.secrets, token, hex.EncodeToString(digest[:]))
	forbidden.Store([]string{token, hex.EncodeToString(digest[:]), grant, h.secrets[0]})
	h.run("multi-discover", 0, "", "discover", "--env", environment)
	described := h.run("multi-describe-runtime", 0, "", "describe", "runtime.get", "--env", environment)
	contract, _ := described["result"].(map[string]any)
	schema, _ := contract["input_schema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	for _, field := range []string{"replica", "owner_query_group", "expected_incarnation"} {
		if _, found := properties[field]; !found {
			t.Fatalf("describe did not publish %s", field)
		}
	}
	if store.reads.Load() != 0 {
		t.Fatal("discover/describe accessed routing infrastructure")
	}

	// Advance the real Redis deadline to the renewal window without sleeping.
	keys, err := h.redis.Keys(context.Background(), "fixture-auth.cli:*:session:*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatal("expected one isolated Redis session")
	}
	const due = `local t = redis.call('TIME')
local record = cjson.decode(redis.call('GET', KEYS[1]))
record.expires_at_ms = tonumber(t[1])*1000 + math.floor(tonumber(t[2])/1000) + 300000
redis.call('SET', KEYS[1], cjson.encode(record), 'PX', 3600000)
return 1`
	if err := h.redis.Eval(context.Background(), due, keys).Err(); err != nil {
		t.Fatal("cannot make fixture session due")
	}
	beforeRequests := h.requestCount()
	result := h.run("multi-replica", 0, "", "invoke", "runtime.get", "--env", environment, "--input", `{"replica":"worker"}`)
	assertTargetReceipt(t, h, result, "worker-boot-1")
	if !containsJSON(result["next_call"], `"expected_incarnation":"worker-boot-1"`) || !containsJSON(result["next_call"], `"replica":"worker"`) {
		t.Fatal("follow-up call did not pin the answering process")
	}
	if h.requestCount()-beforeRequests != 2 || nodes["leader"].rpcCalls.Load() != 1 || nodes["worker"].rpcCalls.Load() != 1 || nodes["worker"].runs.Load() != 1 {
		t.Fatal("invoke did not use one describe, one ingress request and two control RPC hops")
	}
	meta := result["meta"].(map[string]any)
	session, _ := meta["session"].(map[string]any)
	if session["renewed"] != true || nodes["entry"].auth.admissions.Load() != 1 || nodes["entry"].auth.renewals.Load() != 1 || nodes["leader"].auth.admissions.Load() != 0 || nodes["worker"].auth.admissions.Load() != 0 {
		t.Fatal("due CLI session was not renewed exactly once at ingress")
	}
	current, err := h.manager.Authenticate(context.Background(), token)
	if err != nil || time.Until(current.ExpiresAt) < 59*time.Minute {
		t.Fatal("renewal was not persisted in Redis")
	}
	result = h.run("multi-owner", 0, "", "invoke", "runtime.get", "--env", environment, "--input", `{"owner_query_group":"fixture-qg"}`)
	assertTargetReceipt(t, h, result, "worker-boot-1")
	owner, _ := result["meta"].(map[string]any)["owner"].(map[string]any)
	if owner["owner_id"] != "worker" || owner["owner_epoch"] != float64(19) || owner["observed_at"] == nil || owner["deadline"] == nil {
		t.Fatal("lease observation is missing from target receipt")
	}
	result = h.run("multi-worker-source", 0, "", "invoke", "strategy.config", "--env", environment, "--input", `{"replica":"worker","view":"source","strategy_id":"7"}`)
	assertTargetReceipt(t, h, result, "worker-boot-1")
	if !containsJSON(result, `"threshold":91`) || containsJSON(result, `"threshold":80`) {
		t.Fatal("targeted store read used ingress binding")
	}

	beforeRouting := store.reads.Load()
	result = h.run("multi-shared-fleet", 0, "", "invoke", "fleet.get", "--env", environment)
	if store.reads.Load() != beforeRouting || result["meta"].(map[string]any)["answered_by"] != "entry" {
		t.Fatal("shared Fleet query routed")
	}
	nodes["worker"].channel.Store(makeChannel("worker", "worker-boot-2", false))
	beforeRuns := nodes["worker"].runs.Load()
	result = h.run("multi-restarted-worker", 1, "", "invoke", "runtime.get", "--env", environment, "--input", `{"replica":"worker","expected_incarnation":"worker-boot-1"}`)
	assertRoutingError(t, result, "target_changed")
	if nodes["worker"].runs.Load() != beforeRuns {
		t.Fatal("restarted worker executed despite incarnation mismatch")
	}
	result = h.run("multi-new-incarnation", 0, "", "invoke", "runtime.get", "--env", environment, "--input", `{"replica":"worker","expected_incarnation":"worker-boot-2"}`)
	assertTargetReceipt(t, h, result, "worker-boot-2")
	nodes["worker"].channel.Store(makeChannel("worker", "worker-boot-2", true))
	beforeRuns = nodes["worker"].runs.Load()
	result = h.run("multi-mixed-catalog", 1, "", "invoke", "runtime.get", "--env", environment, "--input", `{"replica":"worker"}`)
	assertRoutingError(t, result, "target_operation_contract_mismatch")
	if nodes["worker"].runs.Load() != beforeRuns || nodes["entry"].runs.Load() != 0 || nodes["leader"].runs.Load() != 0 {
		t.Fatal("failed target executed or silently fell back")
	}
	if nodes["entry"].auth.renewals.Load() != 1 || nodes["leader"].auth.admissions.Load() != 0 || nodes["worker"].auth.admissions.Load() != 0 {
		t.Fatal("control RPC touched the CLI session")
	}
	h.run("multi-status", 0, "", "auth", "status", "--env", environment)
	h.run("multi-logout", 0, "", "auth", "logout", "--env", environment)
	if _, err := h.manager.Authenticate(context.Background(), token); cliauth.ErrorCode(err) != "auth_expired_or_revoked" {
		t.Fatal("logout did not revoke routed session")
	}
	h.run("multi-after-logout", 1, "", "discover", "--env", environment)
	h.assertNoSecrets()
}

func assertTargetReceipt(t *testing.T, h *harness, result map[string]any, incarnation string) {
	t.Helper()
	if len(h.steps) == 0 || h.steps[len(h.steps)-1].ResultFile == "" {
		t.Fatal("target result was not persisted")
	}
	meta, _ := result["meta"].(map[string]any)
	if meta["answered_by"] != "worker" || meta["incarnation"] != incarnation || meta["build"] != "worker-build" || !containsJSON(meta["via"], `["entry","leader"]`) || textField(meta, "catalog_revision") == "" || textField(meta, "responded_at") == "" {
		t.Fatal("full result_file lost target identity, route or response metadata")
	}
}

func assertRoutingError(t *testing.T, result map[string]any, code string) {
	t.Helper()
	failure, _ := result["error"].(map[string]any)
	if result["status"] != "error" || failure["code"] != code {
		t.Fatalf("expected explicit routing error %s", code)
	}
	if (code == "target_catalog_mismatch" || code == "target_operation_contract_mismatch") && strings.Contains(stringMustJSON(result["next_call"]), `"describe"`) {
		t.Fatal("catalog mismatch suggested an ingress describe retry loop")
	}
}

func stringMustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
