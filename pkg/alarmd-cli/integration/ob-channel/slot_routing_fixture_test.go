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
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evidenceroute"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// Slot tests supply only domain fixtures. The CLI, entry authorization,
// router, internal worker authentication and gRPC transport are real.
type slotRoutingFixture struct {
	h         *harness
	store     *routingFixtureStore
	nodes     map[string]*routingNode
	forbidden atomic.Value
	mu        sync.Mutex
	receipts  []rpcReceipt
}

func newSlotRoutingFixture(t *testing.T, h *harness, operations func(string) []obchannel.Operation) *slotRoutingFixture {
	t.Helper()
	now := time.Now().UTC()
	s := &slotRoutingFixture{h: h, nodes: map[string]*routingNode{}, store: &routingFixtureStore{workers: map[string]ownership.WorkerRegistration{}, leader: ownership.QueryGroupOwner{OwnerID: "leader", OwnerEpoch: 7, Deadline: now.Add(time.Hour), ObservedAt: now}, owner: ownership.QueryGroupOwner{OwnerID: "worker", OwnerEpoch: 19, Deadline: now.Add(time.Hour), ObservedAt: now}}}
	s.forbidden.Store([]string{})
	for _, id := range []string{"entry", "leader", "worker"} {
		node := &routingNode{auth: &routingAuth{Manager: h.manager}}
		s.nodes[id] = node
		channel, err := obchannel.New(obchannel.Options{Auth: node.auth, EnvironmentID: environment, Replica: id, Build: id + "-build", Incarnation: id + "-boot-1", Operations: operations(id), Route: func(ctx context.Context, invocation obchannel.Invocation) obchannel.Response {
			return node.router.Invoke(ctx, invocation)
		}})
		if err != nil {
			t.Fatal("cannot initialize Slot fixture channel")
		}
		node.channel.Store(channel)
		streamSecret := "constructed-slot-" + id + "-control-credential"
		h.secrets = append(h.secrets, streamSecret)
		node.router, err = evidenceroute.New(evidenceroute.Options{Store: s.store, WorkerID: id, StreamToken: streamSecret, EnvironmentID: environment, Build: id + "-build", Incarnation: id + "-boot-1", CatalogRevision: channel.CatalogRevision(), Execute: func(ctx context.Context, invocation obchannel.Invocation) obchannel.Response {
			return node.channel.Load().ExecuteEvidence(ctx, invocation)
		}})
		if err != nil {
			t.Fatal("cannot initialize Slot control router")
		}
		control, err := viewstream.NewServer(routingAdmission{}, observability.NopObserver{}, viewstream.ServerOptions{})
		if err != nil {
			t.Fatal("cannot initialize Slot fixture ControlService")
		}
		control.SetEvidenceHandler(func(ctx context.Context, request *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
			node.rpcCalls.Add(1)
			raw, _ := proto.Marshal(request)
			for _, secret := range s.forbidden.Load().([]string) {
				if secret != "" && bytes.Contains(raw, []byte(secret)) {
					t.Error("CLI or UQ credential entered control RPC payload")
				}
			}
			s.mu.Lock()
			s.receipts = append(s.receipts, rpcReceipt{Receiver: id, Caller: request.WorkerId, Phase: request.Phase, Target: request.TargetWorkerId})
			s.mu.Unlock()
			return node.router.Handle(ctx, request)
		})
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal("cannot listen for Slot fixture control RPC")
		}
		server := grpc.NewServer()
		pb.RegisterControlServiceServer(server, control)
		go server.Serve(listener)
		t.Cleanup(server.Stop)
		s.store.workers[id] = ownership.WorkerRegistration{WorkerID: id, StreamToken: streamSecret, Endpoint: listener.Addr().String(), ExpiresAt: now.Add(time.Hour)}
	}
	// Setup finishes before any CLI request. Keep the existing direct admin-key
	// grant and Redis session handlers; replace only its channel registration.
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
		s.nodes["entry"].channel.Load().ServeHTTP(w, r)
	})
	return s
}

func (s *slotRoutingFixture) report(queryReceipts []slotUQReceipt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "passed"
	if s.h.t.Failed() {
		status = "failed"
	}
	report := map[string]any{"status": status, "rpc_receipts": s.receipts, "uq_requests": queryReceipts, "routing_store_reads": s.store.reads.Load(), "fixture_boundary": "Frozen Slot references, native object records, registry and active leases are constructed fixtures. CLI, Redis-backed session authorization, OB operations, control RPC routing, UQ request construction and decoding are real. This is not production alert replay or business acceptance."}
	for id, node := range s.nodes {
		report[id] = map[string]int32{"rpc_calls": node.rpcCalls.Load(), "session_admissions": node.auth.admissions.Load(), "session_renewals": node.auth.renewals.Load()}
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	for _, secret := range s.h.secrets {
		if secret != "" && bytes.Contains(raw, []byte(secret)) {
			s.h.t.Error("refusing to write Slot report containing a credential")
			return
		}
	}
	if err := os.WriteFile(filepath.Join(s.h.dir, "slot-report.json"), append(raw, '\n'), 0600); err != nil {
		s.h.t.Error("cannot save Slot acceptance report")
	}
}
