// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
)

type scopedTestAuth struct {
	testAuth
	scope string
}

func (a *scopedTestAuth) Authenticate(ctx context.Context, token string) (cliauth.Session, error) {
	s, err := a.testAuth.Authenticate(ctx, token)
	s.Scope = a.scope
	return s, err
}

// An execution operation runs for the deployment's one operations session -
// and for a session an earlier build granted as an execution session - with
// no further scope; a session with no scope the channel knows is refused
// before admission; and the internal Worker route never reaches it.
func TestExecRunsForTheOperationsSessionAndNeverThroughInternalRPC(t *testing.T) {
	auth := &scopedTestAuth{scope: ""}
	var runs atomic.Int32
	op := Operation{ID: "pod.exec", Summary: "Execute", Effect: "exec", Run: func(context.Context, Params) Outcome { runs.Add(1); return Outcome{Complete: true} }}
	c, err := New(Options{Auth: auth, EnvironmentID: "test", Replica: "worker", Operations: []Operation{op}})
	if err != nil {
		t.Fatal(err)
	}
	status, response := call(t, c, envelope(c, "invoke", op.ID, nil))
	if status != 403 || response.Error.Code != "permission_denied" || runs.Load() != 0 || auth.calls.Load() != 0 {
		t.Fatalf("a session without a known scope was admitted: %+v", response)
	}
	auth.scope = cliauth.ScopeReadonly
	_, discovery := call(t, c, envelope(c, "discover", "", nil))
	row := discovery.Result.(map[string]any)["operations"].([]any)[0].(map[string]any)
	if row["authorized"] != true || row["required_scope"] != cliauth.ScopeReadonly {
		t.Fatalf("the operations session is not shown as authorized: %+v", row)
	}
	auth.scope = cliauth.ScopeReadonly
	before := runs.Load()
	if status, response := call(t, c, envelope(c, "invoke", op.ID, nil)); status != 200 || runs.Load() != before+1 {
		t.Fatalf("the operations session refused: %+v", response)
	}
	// A scope no running build issued is not a session.
	auth.scope = "deployment_ops_exec"
	before = runs.Load()
	if status, _ := call(t, c, envelope(c, "invoke", op.ID, nil)); status == 200 || runs.Load() != before {
		t.Fatal("a session with the execution scope ran an operation")
	}
	runsBefore := runs.Load()
	response = c.ExecuteEvidence(context.Background(), Invocation{EnvironmentID: "test", Version: Version, Revision: c.revision, Operation: op.ID, Target: Target{Replica: "worker"}})
	if response.Error == nil || response.Error.Code != "operation_not_internal_evidence" || runs.Load() != runsBefore {
		t.Fatalf("internal exec bypass: %+v", response)
	}
}

func TestNestedObjectAndArrayBoundaries(t *testing.T) {
	item := Field{Type: "object", Properties: map[string]Field{"id": {Type: "string", MinLength: 1}, "value": {Type: "number"}}, Required: []string{"id"}}
	op := Operation{ID: "uq.query", Summary: "Query", Fields: map[string]Field{"body": {Type: "object", Properties: map[string]Field{"queries": {Type: "array", MinItems: 1, MaxItems: 2, Items: &item}}, Required: []string{"queries"}}}, Required: []string{"body"}, Run: func(context.Context, Params) Outcome { return Outcome{Complete: true} }}
	c := testChannel(t, &testAuth{}, op)
	for _, invalid := range []string{`{}`, `{"body":null}`, `{"body":[]}`, `{"body":{}}`, `{"body":{"queries":[]}}`, `{"body":{"queries":[{"id":"x","extra":1}]}}`, `{"body":{"queries":[{"value":1}]}}`, `{"body":{"queries":[{"id":"x","value":"1"}]}}`, `{"body":{"queries":[{"id":"x"},{"id":"y"},{"id":"z"}]}}`, `{"body":{"queries":[{"id":"x"}],"extra":true}}`} {
		var params Params
		d := json.NewDecoder(strings.NewReader(invalid))
		d.UseNumber()
		_ = d.Decode(&params)
		status, response := call(t, c, envelope(c, "invoke", op.ID, params))
		if status != 400 || response.Error.Code != "invalid_input" {
			t.Fatalf("invalid nested input passed: %s %+v", invalid, response)
		}
	}
	params := Params{"body": map[string]any{"queries": []any{map[string]any{"id": "x", "value": json.Number("1.5")}}}}
	if status, response := call(t, c, envelope(c, "invoke", op.ID, params)); status != 200 {
		t.Fatalf("valid nested rejected: %+v", response)
	}
	open := Field{Type: "object", AdditionalProperties: true}
	if err := validateField(open, map[string]any{"native": []any{map[string]any{"x": nil}}}); err != nil {
		t.Fatal(err)
	}
	schema, _ := json.Marshal(op.Fields["body"])
	if !strings.Contains(string(schema), `"additionalProperties":false`) {
		t.Fatalf("object boundary omitted: %s", schema)
	}
}

func TestProviderPoolsDoNotBlockShortEvidenceReads(t *testing.T) {
	auth := &scopedTestAuth{scope: cliauth.ScopeReadonly}
	run := func(ctx context.Context, _ Params) Outcome {
		<-ctx.Done()
		return Outcome{Value: map[string]any{"remote_state": "unknown"}}
	}
	operations := []Operation{{ID: "read", Summary: "Read", Run: func(context.Context, Params) Outcome { return Outcome{Complete: true} }}, {ID: "uq.query", Summary: "Query", ExecutionPool: "sre", Run: run}, {ID: "pod.exec", Summary: "Exec", Effect: "exec", Run: run}}
	c, err := New(Options{Auth: auth, EnvironmentID: "test", Operations: operations})
	if err != nil {
		t.Fatal(err)
	}
	// Occupied provider permits do not contend for the existing reader pool.
	c.sreSlots <- struct{}{}
	c.execSlots <- struct{}{}
	defer func() { <-c.sreSlots; <-c.execSlots }()
	if status, response := call(t, c, envelope(c, "invoke", "read", nil)); status != 200 {
		t.Fatalf("provider blocked read: %+v", response)
	}
	for _, id := range []string{"uq.query", "pod.exec"} {
		if status, response := call(t, c, envelope(c, "invoke", id, nil)); status != 429 || response.Error.Code != "request_budget_exceeded" {
			t.Fatalf("provider pool did not bound %s: %+v", id, response)
		}
	}
}

type requestDeadlineRecorder struct {
	*httptest.ResponseRecorder
	read, write []time.Time
}

func (w *requestDeadlineRecorder) SetReadDeadline(t time.Time) error {
	w.read = append(w.read, t)
	return nil
}
func (w *requestDeadlineRecorder) SetWriteDeadline(t time.Time) error {
	w.write = append(w.write, t)
	return nil
}

func TestOperationDeadlineAndExecReceiptSurviveCancellation(t *testing.T) {
	auth := &scopedTestAuth{scope: cliauth.ScopeReadonly}
	// The operation's deadline is read off its context and judged against
	// two instants the test takes around the call - after the request starts
	// and once it has returned - never against how much time was left when
	// the operation happened to be scheduled, which load shrinks.
	var deadline time.Time
	var hasDeadline bool
	op := Operation{ID: "pod.exec", Summary: "Exec", Effect: "exec", ExecutionTimeout: 20 * time.Millisecond, Run: func(ctx context.Context, _ Params) Outcome {
		deadline, hasDeadline = ctx.Deadline()
		<-ctx.Done()
		return Outcome{Value: map[string]any{"remote_state": "unknown", "stdout": "captured"}, Error: &Failure{Code: "remote_state_unknown", Message: "Remote termination unconfirmed."}}
	}}
	c, err := New(Options{Auth: auth, EnvironmentID: "test", Operations: []Operation{op}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(envelope(c, "invoke", op.ID, nil))
	r := httptest.NewRequest(http.MethodPost, "/api/cli/channel", strings.NewReader(string(data)))
	r.Header.Set("Authorization", "Bearer fixture")
	w := &requestDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	start := time.Now()
	c.ServeHTTP(w, r)
	returned := time.Now()
	var out Response
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !hasDeadline || deadline.Before(start.Add(op.ExecutionTimeout)) || deadline.After(returned) || out.Error == nil || out.Error.Code != "remote_state_unknown" || out.Result == nil || out.Evidence.Complete {
		t.Fatalf("timeout receipt lost: deadline %v (start %v, returned %v) %+v", deadline, start, returned, out)
	}
	if len(w.write) < 3 || w.write[1].Sub(start) < TransportMargin || !w.write[len(w.write)-1].IsZero() || !w.read[len(w.read)-1].IsZero() {
		t.Fatalf("deadlines mismatch: read %v write %v", w.read, w.write)
	}
	_, description := call(t, c, envelope(c, "describe", op.ID, nil))
	limits := description.Result.(map[string]any)["request_limits"].(map[string]any)
	if limits["execution_timeout_ms"] != float64(20) || limits["transport_margin_ms"] != float64(5000) || limits["budget_scope"] != "process" {
		t.Fatalf("limit metadata: %+v", limits)
	}
}

func TestRequestBytesAndInvalidRegistration(t *testing.T) {
	op := Operation{ID: "read", Summary: "Read", RequestMaxBytes: 512, Fields: map[string]Field{"text": {Type: "string"}}, Run: func(context.Context, Params) Outcome { return Outcome{Complete: true} }}
	c := testChannel(t, &testAuth{}, op)
	if status, out := call(t, c, envelope(c, "invoke", op.ID, Params{"text": strings.Repeat("x", 512)})); status != 400 || out.Error.Code != "invalid_input" {
		t.Fatal("per-operation request limit omitted")
	}
	for _, change := range []func(*Operation){func(o *Operation) { o.ExecutionTimeout = MaxExecutionTimeout + time.Second }, func(o *Operation) { o.Effect = "exec"; o.RequiredScope = "deployment_ops_exec" }, func(o *Operation) { o.ExecutionPool = "unknown" }, func(o *Operation) {
		o.Fields = map[string]Field{"body": {Type: "object", Required: []string{"missing"}}}
	}} {
		invalid := op
		change(&invalid)
		if _, err := NewEvidenceExecutor(ExecutorOptions{EnvironmentID: "test", Operations: []Operation{invalid}}); err == nil {
			t.Fatal("invalid registration accepted")
		}
	}
}

func TestLegacyEvidenceExecutionDigestRemainsCompatible(t *testing.T) {
	// This digest was independently computed by the same fixture against
	// readonly baseline a97ea19914692e72db82efcf2bd29c3e111fa1db.
	op := Operation{ID: "read", Summary: "Read", Targetable: true, Fields: map[string]Field{"id": {Type: "string", MinLength: 1, MaxLength: 128}}, Required: []string{"id"}, Run: func(context.Context, Params) Outcome { return Outcome{Complete: true} }}
	e, err := NewEvidenceExecutor(ExecutorOptions{EnvironmentID: "fixture", Operations: []Operation{op}})
	if err != nil {
		t.Fatal(err)
	}
	if digest := e.OperationContractRevision("read"); digest != "a13c5b13c82c701c2ebcf28fc596e84a26aa7e3890f45c3f50bdf185e3a61d42" {
		t.Fatalf("legacy default read contract changed: %s", digest)
	}
}
