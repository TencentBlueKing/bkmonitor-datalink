// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package evidenceroute

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

type fixtureStore struct {
	mu         sync.Mutex
	workers    map[string]ownership.WorkerRegistration
	leader     ownership.QueryGroupOwner
	owner      ownership.QueryGroupOwner
	ownerReads int
	moveOnRead int
	// leaderReads counts lease reads; the read numbered moveLeaderOnRead
	// sees the next term, as if the lease was handed over just before it.
	leaderReads      int
	moveLeaderOnRead int
}

func (s *fixtureStore) ReadWorker(_ context.Context, id string) (ownership.WorkerRegistration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[id]
	return w, ok, nil
}
func (s *fixtureStore) ReadActiveControlLeader(context.Context) (ownership.QueryGroupOwner, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leaderReads++
	if s.moveLeaderOnRead == s.leaderReads {
		s.leader.OwnerEpoch++
	}
	return s.leader, s.leader.OwnerID != "", nil
}
func (s *fixtureStore) ReadQueryGroupOwner(context.Context, execution.QueryGroupIdentity) (ownership.QueryGroupOwner, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownerReads++
	if s.moveOnRead == s.ownerReads {
		s.owner.OwnerEpoch++
	}
	return s.owner, s.owner.OwnerID != "", nil
}

type fixtureAuth struct{ admissions atomic.Int32 }

func (*fixtureAuth) Authenticate(_ context.Context, token string) (cliauth.Session, error) {
	if token != "cli-secret-fixture" {
		return cliauth.Session{}, &cliauth.Error{Code: "unauthorized", HTTPStatus: 401}
	}
	return cliauth.Session{ID: "session-fixture", EnvironmentID: "fixture", Scope: cliauth.ScopeReadonly, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (a *fixtureAuth) Admit(_ context.Context, s cliauth.Session, renew bool) (cliauth.Session, error) {
	a.admissions.Add(1)
	s.Renewed = renew
	return s, nil
}

type fixtureAdmission struct{}

func (fixtureAdmission) Admit(context.Context, string, string) (string, error) { return "", nil }

type fixtureNode struct {
	router   *Router
	channel  *obchannel.Channel
	auth     *fixtureAuth
	runs     atomic.Int32
	server   *grpc.Server
	rpcCalls atomic.Int32
}

func fixture(t *testing.T) (map[string]*fixtureNode, *fixtureStore) {
	t.Helper()
	store := &fixtureStore{workers: map[string]ownership.WorkerRegistration{}, leader: ownership.QueryGroupOwner{OwnerID: "leader", OwnerEpoch: 7}, owner: ownership.QueryGroupOwner{OwnerID: "worker", OwnerEpoch: 19, Deadline: time.Now().Add(time.Minute), ObservedAt: time.Now()}}
	nodes := map[string]*fixtureNode{}
	for _, id := range []string{"entry", "leader", "worker"} {
		node := &fixtureNode{auth: &fixtureAuth{}}
		op := obchannel.Operation{ID: "runtime.get", Summary: "Read process", EvidenceScope: "process", Targetable: true, Run: func(ctx context.Context, p obchannel.Params) obchannel.Outcome {
			node.runs.Add(1)
			return obchannel.Outcome{Complete: true, Value: map[string]string{"process": id}, Next: []obchannel.Call{{Operation: "runtime.get", Params: obchannel.Params{}}}}
		}}
		shared := obchannel.Operation{ID: "shared.get", Summary: "Read shared", EvidenceScope: "deployment", Run: func(context.Context, obchannel.Params) obchannel.Outcome {
			node.runs.Add(1)
			return obchannel.Outcome{Complete: true}
		}}
		channel, err := obchannel.New(obchannel.Options{Auth: node.auth, EnvironmentID: "fixture", Replica: id, Incarnation: id + "-boot", Build: id + "-build", Operations: []obchannel.Operation{op, shared}, Route: func(ctx context.Context, call obchannel.Invocation) obchannel.Response {
			return node.router.Invoke(ctx, call)
		}})
		if err != nil {
			t.Fatal(err)
		}
		node.channel = channel
		node.router, err = New(Options{Store: store, WorkerID: id, StreamToken: id + "-worker-secret", EnvironmentID: "fixture", Build: id + "-build", Incarnation: id + "-boot", CatalogRevision: channel.CatalogRevision(), Execute: channel.ExecuteEvidence})
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		vs, err := viewstream.NewServer(fixtureAdmission{}, observability.NopObserver{}, viewstream.ServerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		vs.SetEvidenceHandler(func(ctx context.Context, req *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
			node.rpcCalls.Add(1)
			if strings.Contains(req.StreamToken, "cli-secret") {
				t.Error("CLI credential entered control protocol")
			}
			return node.router.Handle(ctx, req)
		})
		node.server = grpc.NewServer()
		pb.RegisterControlServiceServer(node.server, vs)
		go node.server.Serve(listener)
		t.Cleanup(node.server.Stop)
		store.workers[id] = ownership.WorkerRegistration{WorkerID: id, Endpoint: listener.Addr().String(), StreamToken: id + "-worker-secret", ExpiresAt: time.Now().Add(time.Hour)}
		nodes[id] = node
	}
	return nodes, store
}

func invoke(t *testing.T, node *fixtureNode, op string, params obchannel.Params) obchannel.Response {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"channel_version": obchannel.Version, "mode": "invoke", "operation": op, "expected_catalog_revision": node.channel.CatalogRevision(), "params": params, "renew_if_due": true})
	req := httptest.NewRequest("POST", "/api/cli/channel", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer cli-secret-fixture")
	w := httptest.NewRecorder()
	node.channel.ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), "cli-secret") || strings.Contains(w.Body.String(), "worker-secret") {
		t.Fatal("credential exposed in evidence")
	}
	var result obchannel.Response
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestThreeInstancesRouteThroughLeaderAndKeepPublicReadsLocal(t *testing.T) {
	nodes, _ := fixture(t)
	result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "worker"})
	if result.Status != "ok" || result.Meta.AnsweredBy != "worker" || result.Meta.Incarnation != "worker-boot" || result.Meta.Build != "worker-build" || result.Meta.Session == nil || !result.Meta.Session.Renewed {
		t.Fatalf("target evidence lost: %+v", result)
	}
	if strings.Join(result.Meta.Via, ",") != "entry,leader" {
		t.Fatalf("wrong route: %+v", result.Meta.Via)
	}
	if nodes["leader"].rpcCalls.Load() != 1 || nodes["worker"].rpcCalls.Load() != 1 || nodes["worker"].runs.Load() != 1 || nodes["entry"].runs.Load() != 0 {
		t.Fatal("request did not go through Leader to Worker")
	}
	if nodes["entry"].auth.admissions.Load() != 1 || nodes["leader"].auth.admissions.Load() != 0 || nodes["worker"].auth.admissions.Load() != 0 {
		t.Fatal("CLI session escaped the ingress")
	}
	result = invoke(t, nodes["entry"], "shared.get", obchannel.Params{})
	if result.Status != "ok" || result.Meta.AnsweredBy != "entry" || nodes["leader"].rpcCalls.Load() != 1 {
		t.Fatal("shared read was routed")
	}
	result = invoke(t, nodes["entry"], "shared.get", obchannel.Params{"replica": "worker"})
	if result.Error == nil || result.Error.Code != "invalid_input" || nodes["leader"].rpcCalls.Load() != 1 {
		t.Fatal("shared operation accepted routing")
	}
}

func TestOwnerIsRecheckedAtTargetAndNotAnExecutionReceipt(t *testing.T) {
	nodes, store := fixture(t)
	result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"owner_query_group": "qg-fixture"})
	if result.Status != "ok" || result.Meta.Owner == nil || result.Meta.Owner.OwnerID != "worker" || result.Meta.Owner.OwnerEpoch != 19 || result.Meta.Incarnation != "worker-boot" {
		t.Fatalf("owner facts lost: %+v", result)
	}
	store.mu.Lock()
	store.ownerReads = 0
	store.moveOnRead = 2
	store.mu.Unlock()
	result = invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"owner_query_group": "qg-fixture"})
	if result.Error == nil || result.Error.Code != "target_changed" || nodes["worker"].runs.Load() != 1 {
		t.Fatalf("moved owner executed: %+v", result)
	}
	store.mu.Lock()
	store.owner.OwnerID = ""
	store.mu.Unlock()
	result = invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"owner_query_group": "qg-fixture"})
	if result.Error == nil || result.Error.Code != "owner_unavailable" {
		t.Fatalf("absent owner became success: %+v", result)
	}
}

func TestLeaderLocalTargetAndExpiredRegistration(t *testing.T) {
	nodes, store := fixture(t)
	result := invoke(t, nodes["leader"], "runtime.get", obchannel.Params{"replica": "leader"})
	if result.Status != "ok" || result.Meta.AnsweredBy != "leader" || nodes["leader"].runs.Load() != 1 || nodes["leader"].auth.admissions.Load() != 1 {
		t.Fatalf("leader local target failed or double admitted: %+v", result)
	}
	store.mu.Lock()
	worker := store.workers["worker"]
	worker.ExpiresAt = time.Now().Add(-time.Second)
	store.workers["worker"] = worker
	store.mu.Unlock()
	result = invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "worker"})
	if result.Error == nil || result.Error.Code != "target_unavailable" || nodes["worker"].runs.Load() != 0 {
		t.Fatalf("expired registration routed: %+v", result)
	}
}

func TestIncarnationAndCatalogMismatchDoNotExecuteOrFallback(t *testing.T) {
	nodes, _ := fixture(t)
	result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "worker", "expected_incarnation": "old-boot"})
	if result.Error == nil || result.Error.Code != "target_changed" || nodes["worker"].runs.Load() != 0 {
		t.Fatalf("restart not detected: %+v", result)
	}
	call := obchannel.Invocation{EnvironmentID: "fixture", Version: obchannel.Version, Revision: "old-catalog", Operation: "runtime.get", RequestID: "catalog-fixture", Params: obchannel.Params{}, Target: obchannel.Target{Replica: "worker"}}
	result = nodes["entry"].router.Invoke(context.Background(), call)
	if result.Error == nil || result.Error.Code != "target_catalog_mismatch" || nodes["worker"].runs.Load() != 0 {
		t.Fatalf("catalog mismatch executed: %+v", result)
	}
	if nodes["entry"].runs.Load() != 0 || nodes["leader"].runs.Load() != 0 {
		t.Fatal("failed target silently fell back")
	}
}

func TestLeaderAndWorkerIdentityRequiredBeforeExecution(t *testing.T) {
	nodes, store := fixture(t)
	base := &pb.EvidenceRequest{WorkerId: "entry", StreamToken: "entry-worker-secret", Phase: "execute", EnvironmentId: "fixture", RequestId: "rpc-fixture", ChannelVersion: obchannel.Version, CatalogRevision: nodes["entry"].channel.CatalogRevision(), Operation: "runtime.get", ParamsJson: []byte(`{}`), TargetWorkerId: "worker", ControlEpoch: 7}
	_, err := nodes["worker"].router.Handle(context.Background(), base)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("non-Leader executed: %v", err)
	}
	base.WorkerId = "leader"
	base.StreamToken = "wrong"
	_, err = nodes["worker"].router.Handle(context.Background(), base)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bad identity executed: %v", err)
	}
	base.StreamToken = "leader-worker-secret"
	base.EnvironmentId = "other"
	_, err = nodes["worker"].router.Handle(context.Background(), base)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatal("environment was not checked")
	}
	base.EnvironmentId = "fixture"
	base.ControlEpoch = 6
	result, err := nodes["worker"].router.Handle(context.Background(), base)
	if err != nil || !bytes.Contains(result.ResponseJson, []byte("control_changed")) {
		t.Fatalf("old term accepted: %v", err)
	}
	store.mu.Lock()
	store.leader.OwnerID = ""
	store.mu.Unlock()
	response := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "worker"})
	if response.Error == nil || response.Error.Code != "control_unavailable" || nodes["worker"].runs.Load() != 0 {
		t.Fatalf("no Leader fallback: %+v", response)
	}
}

func TestLegacyControlServiceAndUnreachableTarget(t *testing.T) {
	nodes, store := fixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	old := grpc.NewServer()
	pb.RegisterControlServiceServer(old, &pb.UnimplementedControlServiceServer{})
	go old.Serve(listener)
	t.Cleanup(old.Stop)
	store.mu.Lock()
	registration := store.workers["worker"]
	registration.Endpoint = listener.Addr().String()
	store.workers["worker"] = registration
	store.mu.Unlock()
	result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "worker"})
	if result.Error == nil || result.Error.Code != "target_unsupported" {
		t.Fatalf("legacy server not explicit: %+v", result)
	}
	old.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result = nodes["entry"].router.Invoke(ctx, obchannel.Invocation{EnvironmentID: "fixture", Version: obchannel.Version, Revision: nodes["entry"].channel.CatalogRevision(), Operation: "runtime.get", RequestID: "unreachable-fixture", Params: obchannel.Params{}, Target: obchannel.Target{Replica: "worker"}})
	if result.Error == nil || result.Error.Code != "target_timeout" || nodes["worker"].runs.Load() != 0 {
		t.Fatalf("unreachable target became evidence: %+v", result)
	}
}

func TestControlLeaderKeepsItsLeaseWhenTheLeaderCannotBeReached(t *testing.T) {
	nodes, _ := fixture(t)
	nodes["leader"].server.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := nodes["entry"].router.Invoke(ctx, obchannel.Invocation{EnvironmentID: "fixture", Version: obchannel.Version, Revision: nodes["entry"].channel.CatalogRevision(), Operation: "runtime.get", RequestID: "leader-down-fixture", Params: obchannel.Params{}, Target: obchannel.Target{ControlLeader: true}})
	if result.Error == nil || result.Meta.ControlLeader == nil || result.Meta.ControlLeader.OwnerID != "leader" || result.Meta.ControlLeader.OwnerEpoch != 7 {
		t.Fatalf("a transport failure lost the lease it was resolved from: %+v", result)
	}
	if nodes["entry"].runs.Load() != 0 {
		t.Fatal("an unreachable Leader fell back to the ingress")
	}
}

func TestControlLeaderTargetAnswersFromTheLeaseItWasRoutedBy(t *testing.T) {
	nodes, store := fixture(t)
	result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"control_leader": true})
	if result.Status != "ok" || result.Meta.AnsweredBy != "leader" || result.Meta.Incarnation != "leader-boot" {
		t.Fatalf("control_leader was not answered by the Leader: %+v", result)
	}
	if result.Meta.ControlLeader == nil || result.Meta.ControlLeader.OwnerID != "leader" || result.Meta.ControlLeader.OwnerEpoch != 7 {
		t.Fatalf("the resolved lease is not reported: %+v", result.Meta.ControlLeader)
	}
	if strings.Join(result.Meta.Via, ",") != "entry,leader" || nodes["leader"].runs.Load() != 1 || nodes["entry"].runs.Load() != 0 || nodes["worker"].runs.Load() != 0 {
		t.Fatalf("control_leader ran somewhere other than the Leader: via=%v", result.Meta.Via)
	}
	if result.Next[0].Params.String("replica") != "leader" || result.Next[0].Params.String("expected_incarnation") != "leader-boot" {
		t.Fatalf("next call is not pinned to the process that answered: %+v", result.Next)
	}

	// Asked of the Leader itself, it still answers there and says so.
	result = invoke(t, nodes["leader"], "runtime.get", obchannel.Params{"control_leader": true})
	if result.Status != "ok" || result.Meta.AnsweredBy != "leader" || result.Meta.ControlLeader == nil || nodes["leader"].runs.Load() != 2 {
		t.Fatalf("control_leader at the Leader failed: %+v", result)
	}

	// A plain replica read carries no lease claim.
	result = invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "leader"})
	if result.Status != "ok" || result.Meta.ControlLeader != nil {
		t.Fatalf("a replica read claimed a Leader lease: %+v", result.Meta)
	}

	// The lease handed over between the ingress read and the Leader's own
	// check: the read fails, and nobody answers in the old term's place.
	store.mu.Lock()
	store.leaderReads, store.moveLeaderOnRead = 0, 2
	store.mu.Unlock()
	runs := nodes["leader"].runs.Load()
	result = invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"control_leader": true})
	if result.Error == nil || result.Error.Code != "control_changed" || result.Meta.ControlLeader == nil || nodes["leader"].runs.Load() != runs || nodes["worker"].runs.Load() != 0 {
		t.Fatalf("handover mid-read answered or lost its lease facts: %+v", result)
	}

	// A restarted Leader is caught like any other target.
	result = invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"control_leader": true, "expected_incarnation": "old-boot"})
	if result.Error == nil || result.Error.Code != "target_changed" || nodes["leader"].runs.Load() != runs {
		t.Fatalf("restarted Leader answered: %+v", result)
	}

	store.mu.Lock()
	store.leader.OwnerID = ""
	store.mu.Unlock()
	result = invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"control_leader": true})
	if result.Error == nil || result.Error.Code != "control_unavailable" || nodes["entry"].runs.Load() != 0 {
		t.Fatalf("no Leader fell back to the ingress: %+v", result)
	}
}
