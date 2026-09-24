package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
)

type internalAuthProbe struct{ calls atomic.Int32 }

func (a *internalAuthProbe) Authenticate(context.Context, string) (cliauth.Session, error) {
	a.calls.Add(1)
	return cliauth.Session{}, errors.New("internal execution must not authenticate a CLI token")
}
func (a *internalAuthProbe) Admit(context.Context, cliauth.Session, bool) (cliauth.Session, error) {
	a.calls.Add(1)
	return cliauth.Session{}, errors.New("internal execution must not admit or renew a CLI session")
}

func targetOperation(run func(context.Context, Params) Outcome) Operation {
	return Operation{ID: "process.read", Summary: "Read process fixture", EvidenceScope: "process", Targetable: true, Fields: map[string]Field{"id": {Type: "string", MinLength: 1, MaxLength: 32}}, Required: []string{"id"}, Run: run}
}
func newTargetChannel(t *testing.T, options Options) *Channel {
	t.Helper()
	c, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func internalInvocation(c *Channel) Invocation {
	return Invocation{EnvironmentID: c.options.EnvironmentID, Version: Version, Revision: c.CatalogRevision(), Operation: "process.read", RequestID: "entry-request", Params: Params{"id": "object"}, Target: Target{Replica: c.options.Replica, ExpectedIncarnation: c.options.Incarnation}}
}

func TestTargetSchemaAndDomainValidationStayConsistent(t *testing.T) {
	ops := append(NativeOperations(http.NotFoundHandler()), StoreOperations(obevidence.New(obevidence.Options{}))...)
	c := testChannel(t, &testAuth{}, ops...)
	for _, id := range []string{"sample.get", "observation.get", "strategy.config", "store.inspect"} {
		op := c.ops[id]
		if !op.Targetable {
			t.Errorf("%s is not targetable", id)
		}
		if _, exists := op.Fields["replica"]; exists {
			t.Fatal("target injection mutated domain contract")
		}
		input := describe(op)["input_schema"].(map[string]any)
		fields := input["properties"].(map[string]Field)
		for _, name := range []string{"replica", "owner_query_group", "expected_incarnation"} {
			if fields[name].MinLength != 1 || fields[name].MaxLength == 0 {
				t.Errorf("%s is not bounded and nonempty", name)
			}
		}
		raw, _ := json.Marshal(input)
		if !strings.Contains(string(raw), `"not":{"required":["replica","owner_query_group"]}`) || !strings.Contains(string(raw), `"if":{"required":["expected_incarnation"]}`) ||
			!strings.Contains(string(raw), `"not":{"required":["replica","control_leader"]}`) || !strings.Contains(string(raw), `"not":{"required":["owner_query_group","control_leader"]}`) ||
			!strings.Contains(string(raw), `{"required":["control_leader"]}`) || fields["control_leader"].Type != "boolean" {
			t.Fatal("describe omitted target exclusion/dependency rules")
		}
	}
	for _, id := range []string{"fleet.get", "strategy.get", "object.get"} {
		op := c.ops[id]
		if op.Targetable || op.EvidenceScope != "deployment" {
			t.Errorf("%s should remain deployment scope", id)
		}
		fields := describe(op)["input_schema"].(map[string]any)["properties"].(map[string]Field)
		if _, exists := fields["replica"]; exists {
			t.Errorf("%s gained a target parameter", id)
		}
	}
	store := c.ops["store.inspect"]
	for _, tc := range []struct {
		params Params
		valid  bool
	}{
		{Params{"family": "source_strategy", "strategy_id": "7", "replica": "worker-b"}, true},
		{Params{"family": "source_strategy", "strategy_id": "7", "owner_query_group": "qg:7", "expected_incarnation": "process-b"}, true},
		{Params{"family": "source_strategy", "strategy_id": "7", "replica": "worker-b", "owner_query_group": "qg"}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "expected_incarnation": "process-b"}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "replica": ""}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "replica": "tenant:worker-b"}, true},
		{Params{"family": "source_strategy", "strategy_id": "7", "replica": "租户:worker @b"}, true},
		{Params{"family": "source_strategy", "strategy_id": "7", "replica": "worker\n-b"}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "replica": "worker\x7f-b"}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "replica": strings.Repeat("a", 257)}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "replica": "worker-b", "group_id": "invalid-for-family"}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "control_leader": true}, true},
		{Params{"family": "source_strategy", "strategy_id": "7", "control_leader": true, "expected_incarnation": "process-b"}, true},
		{Params{"family": "source_strategy", "strategy_id": "7", "control_leader": true, "replica": "worker-b"}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "control_leader": true, "owner_query_group": "qg"}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "control_leader": false}, false},
		{Params{"family": "source_strategy", "strategy_id": "7", "control_leader": "true"}, false},
	} {
		params, target, err := invocationParams(store, tc.params)
		if (err == nil) != tc.valid {
			t.Fatalf("params=%v err=%v", tc.params, err)
		}
		if tc.valid && (len(params) != 2 || !target.Explicit()) {
			t.Fatal("target was not separated from domain params")
		}
	}
}

func TestRoutedInvokeAdmitsOnceAndPreservesTargetMetadata(t *testing.T) {
	entryAuth := &testAuth{}
	targetAuth := &internalAuthProbe{}
	var entryRuns, targetRuns, routes atomic.Int32
	stamp := time.Date(2026, 9, 22, 4, 0, 0, 0, time.UTC)
	targetOp := targetOperation(func(_ context.Context, p Params) Outcome {
		targetRuns.Add(1)
		if len(p) != 1 || p.String("id") != "object" {
			t.Error("target got routing parameters")
		}
		return Outcome{Complete: true, Value: map[string]any{"served_by": "b"}}
	})
	target := newTargetChannel(t, Options{Auth: targetAuth, EnvironmentID: "test", Replica: "worker-b", Incarnation: "process-b", Build: "build-b", Now: func() time.Time { return stamp }, Operations: []Operation{targetOp}, Route: func(context.Context, Invocation) Response {
		t.Error("internal execution forwarded again")
		return Response{}
	}})
	entryOp := targetOperation(func(context.Context, Params) Outcome { entryRuns.Add(1); return Outcome{Complete: true} })
	// Entry-local resource absence must not hide an available remote reader.
	entryOp.Availability = func() Availability { return Availability{Reason: "entry Redis is unconfigured"} }
	entry := newTargetChannel(t, Options{Auth: entryAuth, EnvironmentID: "test", Replica: "worker-a", Incarnation: "process-a", Build: "build-a", Now: func() time.Time { return stamp.Add(time.Hour) }, Operations: []Operation{entryOp}, Route: func(ctx context.Context, inv Invocation) Response {
		routes.Add(1)
		if inv.EnvironmentID != "test" || inv.Version != Version || inv.RequestID == "" || inv.Target.OwnerQueryGroup != "qg" {
			t.Error("entry lost invocation context")
		}
		raw, _ := json.Marshal(inv)
		if strings.Contains(string(raw), "test-only") || strings.Contains(string(raw), "session_id") {
			t.Error("CLI credential/session crossed internal boundary")
		}
		inv.Target.Replica = "worker-b"
		inv.Target.ExpectedIncarnation = "process-b"
		response := target.ExecuteEvidence(ctx, inv)
		if response.Meta.Session != nil {
			t.Error("internal response contained a CLI session")
		}
		response.Meta.Via = []string{"worker-a"}
		response.Meta.Owner = &OwnerMeta{QueryGroup: "qg", OwnerID: "worker-b", OwnerEpoch: 7, ObservedAt: stamp, Deadline: stamp.Add(time.Minute)}
		return response
	}})
	if entry.CatalogRevision() != target.CatalogRevision() {
		t.Fatal("dynamic availability or build changed catalog")
	}
	status, out := call(t, entry, envelope(entry, "invoke", "process.read", Params{"id": "object", "owner_query_group": "qg"}))
	if status != 200 || out.Status != "ok" || targetRuns.Load() != 1 || entryRuns.Load() != 0 || routes.Load() != 1 || entryAuth.calls.Load() != 1 || targetAuth.calls.Load() != 0 {
		t.Fatalf("incorrect admission/execution: status=%d out=%+v", status, out)
	}
	if out.Meta.AnsweredBy != "worker-b" || out.Meta.Build != "build-b" || out.Meta.Incarnation != "process-b" || !out.Meta.RespondedAt.Equal(stamp) || len(out.Meta.Via) != 1 || out.Meta.Via[0] != "worker-a" || out.Meta.Owner == nil || out.Meta.Owner.OwnerEpoch != 7 {
		t.Fatalf("entry rewrote target metadata: %+v", out.Meta)
	}
	if out.Meta.Session == nil || out.Meta.Session.ID != "test-session" || !out.Meta.Session.Renewed {
		t.Fatal("external response did not attach entry session")
	}
}

func TestExecuteEvidenceRejectsBeforeRunAndDoesNotUseCLIAuth(t *testing.T) {
	auth := &internalAuthProbe{}
	var runs atomic.Int32
	op := targetOperation(func(context.Context, Params) Outcome { runs.Add(1); return Outcome{Complete: true} })
	c := newTargetChannel(t, Options{Auth: auth, EnvironmentID: "test", Replica: "worker-b", Incarnation: "process-b", Operations: []Operation{op, {ID: "fleet.get", Summary: "Common", Run: func(context.Context, Params) Outcome { runs.Add(1); return Outcome{Complete: true} }}}})
	for _, tc := range []struct {
		name, code string
		change     func(*Invocation)
	}{
		{"environment", "target_environment_mismatch", func(i *Invocation) { i.EnvironmentID = "other" }},
		{"version", "unsupported_channel_version", func(i *Invocation) { i.Version = "alarmd-ob/v2" }},
		{"catalog", "target_catalog_mismatch", func(i *Invocation) { i.Revision = "old" }},
		{"replica", "target_changed", func(i *Invocation) { i.Target.Replica = "other" }},
		{"incarnation", "target_changed", func(i *Invocation) { i.Target.ExpectedIncarnation = "old" }},
		{"unknown", "unknown_operation", func(i *Invocation) { i.Operation = "unregistered" }},
		{"common", "operation_not_targetable", func(i *Invocation) { i.Operation = "fleet.get" }},
		{"schema", "invalid_input", func(i *Invocation) { i.Params = Params{"id": true} }},
		{"target-smuggling", "invalid_input", func(i *Invocation) { i.Params["replica"] = "another" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := internalInvocation(c)
			tc.change(&inv)
			response := c.ExecuteEvidence(context.Background(), inv)
			if response.Error == nil || response.Error.Code != tc.code || response.Status != "error" || response.Evidence.Complete || response.Meta.Session != nil || response.Meta.RespondedAt.IsZero() || response.Next == nil || response.Evidence.Limitations == nil {
				t.Fatalf("failure contract: %+v", response)
			}
		})
	}
	if auth.calls.Load() != 0 || runs.Load() != 0 {
		t.Fatal("failed internal request touched auth or operation")
	}
	inv := internalInvocation(c)
	inv.Target.OwnerQueryGroup = "qg"
	response := c.ExecuteEvidence(context.Background(), inv)
	if response.Status != "ok" || runs.Load() != 1 || auth.calls.Load() != 0 {
		t.Fatalf("internal replica+owner context rejected: %+v", response)
	}
}

func TestRemoteCallDoesNotHoldEntryExecutionSlot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	auth := &testAuth{}
	op := targetOperation(func(context.Context, Params) Outcome { close(entered); <-release; return Outcome{Complete: true} })
	c := newTargetChannel(t, Options{Auth: auth, EnvironmentID: "test", Replica: "worker-a", Operations: []Operation{op}, Route: func(context.Context, Invocation) Response {
		return Response{Status: "ok", Evidence: Evidence{Complete: true}, Meta: Meta{Version: Version, Revision: "target-revision", EnvironmentID: "test", AnsweredBy: "worker-b"}}
	}})
	done := make(chan struct{})
	go func() { defer close(done); call(t, c, envelope(c, "invoke", "process.read", Params{"id": "local"})) }()
	<-entered
	status, out := call(t, c, envelope(c, "invoke", "process.read", Params{"id": "remote", "replica": "worker-b"}))
	if status != 200 || out.Meta.AnsweredBy != "worker-b" || auth.calls.Load() != 2 {
		t.Errorf("remote route was blocked by local slot: %d %+v", status, out)
	}
	status, _ = call(t, c, envelope(c, "invoke", "process.read", Params{"id": "other-local"}))
	if status != 429 || auth.calls.Load() != 2 {
		t.Error("remote route released or bypassed local execution budget")
	}
	close(release)
	<-done
}

func TestInvalidRouteAndRevokedAdmissionNeverDispatch(t *testing.T) {
	var routes atomic.Int32
	auth := &testAuth{}
	op := targetOperation(func(context.Context, Params) Outcome { t.Error("unexpected local run"); return Outcome{} })
	c := newTargetChannel(t, Options{Auth: auth, EnvironmentID: "test", Operations: []Operation{op}})
	params := Params{"id": "object", "replica": "worker-b"}
	status, out := call(t, c, envelope(c, "invoke", "process.read", params))
	if status != 503 || out.Error.Code != "target_routing_unavailable" || auth.calls.Load() != 0 {
		t.Fatal("missing router admitted")
	}
	c.options.Route = func(context.Context, Invocation) Response { routes.Add(1); return Response{} }
	invalid := envelope(c, "invoke", "process.read", params)
	invalid["expected_catalog_revision"] = "old"
	call(t, c, invalid)
	call(t, c, envelope(c, "invoke", "process.read", Params{"id": "object", "replica": "worker-b", "owner_query_group": "qg"}))
	if routes.Load() != 0 || auth.calls.Load() != 0 {
		t.Fatal("invalid route input admitted")
	}
	auth.admitErr = &cliauth.Error{Code: "auth_expired_or_revoked", HTTPStatus: 401}
	status, _ = call(t, c, envelope(c, "invoke", "process.read", params))
	if status != 401 || routes.Load() != 0 || auth.calls.Load() != 1 {
		t.Fatal("revoked admission dispatched")
	}
}

func TestInternalExecutionHonorsContextAndSharedExecutionBudget(t *testing.T) {
	entered := make(chan struct{})
	auth := &testAuth{}
	op := targetOperation(func(ctx context.Context, _ Params) Outcome {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > RequestTimeout {
			t.Error("unbounded execution context")
		}
		close(entered)
		<-ctx.Done()
		return Outcome{Complete: true, Value: map[string]any{"points_read_before_timeout": 2}}
	})
	c := newTargetChannel(t, Options{Auth: auth, EnvironmentID: "test", Replica: "worker-b", Incarnation: "process-b", Operations: []Operation{op}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Response, 1)
	go func() { done <- c.ExecuteEvidence(ctx, internalInvocation(c)) }()
	<-entered
	response := c.ExecuteEvidence(context.Background(), internalInvocation(c))
	if response.Error == nil || response.Error.Code != "request_budget_exceeded" {
		t.Fatal("internal read bypassed execution slots")
	}
	status, _ := call(t, c, envelope(c, "invoke", "process.read", Params{"id": "object"}))
	if status != 429 || auth.calls.Load() != 0 {
		t.Fatal("external local read bypassed shared execution slots")
	}
	cancel()
	response = <-done
	if response.Error == nil || response.Error.Code != "request_timeout" || response.Evidence.Complete || response.Result != nil {
		t.Fatal("cancelled read returned evidence beyond the channel deadline")
	}
	response = c.ExecuteEvidence(ctx, internalInvocation(c))
	if response.Error == nil || response.Error.Code != "request_timeout" {
		t.Fatal("cancelled context executed")
	}
}

func TestTargetedNextCallsPinActualProcessWithoutTargetingCommonReads(t *testing.T) {
	original := []Call{{Operation: "process.read", Params: Params{"id": "next"}}, {Operation: "fleet.get", Params: Params{}}, {Operation: "process.read", Params: Params{"id": "same", "replica": "worker-b"}}, {Operation: "process.read", Params: Params{"id": "other", "replica": "worker-c"}}, {Operation: "process.read", Params: Params{"id": "leader", "control_leader": true}}}
	op := targetOperation(func(context.Context, Params) Outcome { return Outcome{Complete: true, Next: original} })
	c := newTargetChannel(t, Options{Auth: &internalAuthProbe{}, EnvironmentID: "test", Replica: "worker-b", Incarnation: "process-b", Operations: []Operation{op, {ID: "fleet.get", Summary: "Common", Run: func(context.Context, Params) Outcome { return Outcome{Complete: true} }}}})
	inv := internalInvocation(c)
	inv.Target.OwnerQueryGroup = "qg"
	out := c.ExecuteEvidence(context.Background(), inv)
	if out.Next[0].Params.String("owner_query_group") != "qg" || out.Next[0].Params.String("replica") != "" || out.Next[0].Params.String("expected_incarnation") != "process-b" {
		t.Fatal("owner-directed next call lost actual process pin")
	}
	if len(out.Next[1].Params) != 0 || out.Next[2].Params.String("expected_incarnation") != "process-b" || out.Next[3].Params.String("expected_incarnation") != "" {
		t.Fatal("next call targeted a common or different process")
	}
	if len(out.Next[4].Params) != 2 || out.Next[4].Params.String("expected_incarnation") != "" || out.Next[4].Params.String("owner_query_group") != "" {
		t.Fatal("a next call for the Leader was pinned to this process instead of resolved when made")
	}
	if len(original[0].Params) != 1 || len(original[2].Params) != 2 {
		t.Fatal("next-call pin mutated producer-owned data")
	}
}

func TestInternalEvidenceUsesSameResponseBudget(t *testing.T) {
	op := targetOperation(func(context.Context, Params) Outcome {
		return Outcome{Complete: true, Value: strings.Repeat("x", MaxResponseBytes)}
	})
	c := newTargetChannel(t, Options{Auth: &internalAuthProbe{}, EnvironmentID: "test", Replica: "worker-b", Incarnation: "process-b", Operations: []Operation{op}})
	out := c.ExecuteEvidence(context.Background(), internalInvocation(c))
	if out.Error == nil || out.Error.Code != "response_budget_exceeded" || out.Evidence.Complete || out.Result != nil || out.Meta.RespondedAt.IsZero() {
		t.Fatalf("unbounded internal evidence: %+v", out)
	}
}
