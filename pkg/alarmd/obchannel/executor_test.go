// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestEvidenceExecutorAcceptsSameOperationAcrossRoleCatalogs(t *testing.T) {
	var runs atomic.Int32
	op := targetOperation(func(context.Context, Params) Outcome { runs.Add(1); return Outcome{Complete: true} })
	entry, err := NewEvidenceExecutor(ExecutorOptions{EnvironmentID: "test", Replica: "channel", Operations: []Operation{op,
		{ID: "entry.extra", Summary: "Entry-only operation", Run: func(context.Context, Params) Outcome { return Outcome{} }}}})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewEvidenceExecutor(ExecutorOptions{EnvironmentID: "test", Replica: "worker", Incarnation: "boot", Operations: []Operation{op}})
	if err != nil {
		t.Fatal(err)
	}
	if entry.CatalogRevision() == worker.CatalogRevision() {
		t.Fatal("different role catalogs have the same revision")
	}
	inv := Invocation{EnvironmentID: "test", Version: Version, Revision: entry.CatalogRevision(), OperationContractRevision: entry.OperationContractRevision(op.ID),
		Operation: op.ID, RequestID: "read", Params: Params{"id": "object"}, Target: Target{Replica: "worker", ExpectedIncarnation: "boot"}}
	response := worker.ExecuteEvidence(context.Background(), inv)
	if response.Status != "ok" || runs.Load() != 1 || response.Meta.Session != nil {
		t.Fatalf("role-specific catalog rejected: %+v", response)
	}
	inv.OperationContractRevision = ""
	response = worker.ExecuteEvidence(context.Background(), inv)
	if response.Error == nil || response.Error.Code != "target_catalog_mismatch" || runs.Load() != 1 {
		t.Fatalf("legacy whole-catalog check bypassed: %+v", response)
	}
}

func TestOperationContractMismatchNeverRuns(t *testing.T) {
	var runs atomic.Int32
	op := targetOperation(func(context.Context, Params) Outcome { runs.Add(1); return Outcome{Complete: true} })
	entry, err := NewEvidenceExecutor(ExecutorOptions{EnvironmentID: "test", Operations: []Operation{op}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*Operation)
	}{
		{"semantics", func(o *Operation) { o.ContractVersion = "2" }},
		{"schema", func(o *Operation) { o.Fields = map[string]Field{"id": {Type: "string", MaxLength: 16}} }},
		{"limits", func(o *Operation) { o.Limits = map[string]int{"items": 10} }},
		{"targetability", func(o *Operation) { o.Targetable = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := op
			test.change(&changed)
			worker, err := NewEvidenceExecutor(ExecutorOptions{EnvironmentID: "test", Replica: "worker", Operations: []Operation{changed}})
			if err != nil {
				t.Fatal(err)
			}
			response := worker.ExecuteEvidence(context.Background(), Invocation{EnvironmentID: "test", Version: Version, Revision: worker.CatalogRevision(),
				OperationContractRevision: entry.OperationContractRevision(op.ID), Operation: op.ID, RequestID: "read", Params: Params{"id": "x"}, Target: Target{Replica: "worker"}})
			if response.Error == nil || response.Error.Code != "target_operation_contract_mismatch" || runs.Load() != 0 {
				t.Fatalf("changed contract ran: %+v", response)
			}
		})
	}
}

func TestHTTPChannelStillRequiresAuthAndSharesExecutor(t *testing.T) {
	op := targetOperation(func(context.Context, Params) Outcome { return Outcome{Complete: true} })
	executor, err := NewEvidenceExecutor(ExecutorOptions{EnvironmentID: "test", Replica: "worker", Operations: []Operation{op}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{EnvironmentID: "test", Replica: "worker", Executor: executor}); err == nil {
		t.Fatal("HTTP authorization became optional")
	}
	channel, err := New(Options{Auth: &testAuth{}, EnvironmentID: "test", Replica: "worker", Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	if channel.EvidenceExecutor != executor || channel.slots != executor.slots {
		t.Fatal("HTTP and RPC use different execution budgets")
	}
}

func TestChannelDefaultLeaderKeepsDomainReplicaFilter(t *testing.T) {
	var routes atomic.Int32
	auth := &testAuth{}
	op := Operation{ID: "object.list", Summary: "List filtered objects", DefaultControlLeader: true,
		Fields: map[string]Field{"replica": {Type: "string", MinLength: 1}},
		Run: func(context.Context, Params) Outcome {
			t.Fatal("default Leader read ran at the ingress")
			return Outcome{}
		}}
	channel, err := New(Options{Auth: auth, EnvironmentID: "test", Replica: "channel", Operations: []Operation{op}, RouteControlDefaults: true,
		Route: func(_ context.Context, invocation Invocation) Response {
			routes.Add(1)
			if !invocation.Target.ControlLeader || invocation.Target.Replica != "" || invocation.Params.String("replica") != "worker-filter" || invocation.OperationContractRevision == "" {
				t.Fatalf("domain replica was reinterpreted as a target: %+v", invocation)
			}
			return Response{Status: "ok", Evidence: Evidence{Complete: true}, Meta: Meta{Version: Version, Revision: "leader-catalog", EnvironmentID: "test", AnsweredBy: "leader"}}
		}})
	if err != nil {
		t.Fatal(err)
	}
	status, response := call(t, channel, envelope(channel, "invoke", op.ID, Params{"replica": "worker-filter"}))
	if status != 200 || routes.Load() != 1 || response.Meta.Revision != channel.CatalogRevision() || auth.calls.Load() != 1 {
		t.Fatalf("incorrect Leader entry admission: %d %+v", status, response)
	}
}

func TestChannelWorkerTargetRequirementRejectsBeforeAdmission(t *testing.T) {
	var routes atomic.Int32
	auth := &testAuth{}
	op := Operation{ID: "lookback.get", Summary: "Read Worker lookback", Targetable: true,
		Run: func(context.Context, Params) Outcome {
			t.Fatal("channel answered a fake Worker read")
			return Outcome{}
		}}
	channel, err := New(Options{Auth: auth, EnvironmentID: "test", Replica: "channel", Operations: []Operation{op}, RequireWorkerTarget: map[string]bool{op.ID: true},
		Route: func(context.Context, Invocation) Response {
			routes.Add(1)
			return Response{Status: "ok", Evidence: Evidence{Complete: true}}
		}})
	if err != nil {
		t.Fatal(err)
	}
	status, response := call(t, channel, envelope(channel, "invoke", op.ID, Params{}))
	if status != 400 || response.Error == nil || response.Error.Code != "worker_target_required" || auth.calls.Load() != 0 || routes.Load() != 0 {
		t.Fatalf("untargeted Worker read admitted: %d %+v", status, response)
	}
	status, response = call(t, channel, envelope(channel, "invoke", op.ID, Params{"replica": "worker"}))
	if status != 200 || response.Status != "ok" || auth.calls.Load() != 1 || routes.Load() != 1 {
		t.Fatalf("explicit Worker target rejected: %d %+v", status, response)
	}
}
