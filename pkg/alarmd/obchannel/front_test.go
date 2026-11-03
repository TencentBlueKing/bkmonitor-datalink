// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

func frontRead(t *testing.T, c *Channel, op string) (int, Response) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"channel_version": Version, "mode": "invoke", "operation": op, "params": map[string]any{}})
	r := httptest.NewRequest(http.MethodPost, cliauth.FrontReadPath, bytes.NewReader(raw))
	w := httptest.NewRecorder()
	c.FrontReadHandler(func(*http.Request) error { return nil }).ServeHTTP(w, r)
	var out Response
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("front read of %s answered %d %s", op, w.Code, w.Body.String())
	}
	return w.Code, out
}

// Front reads are never refused for a budget: the page's reads, a burst of
// list reads included, all answer. Before, past 2.5% of the CPU limit a
// minute the list reads were refused as front_rate_limited.
func TestFrontReadsAreNeverRefusedForABudget(t *testing.T) {
	const together = 32
	// A barrier: each read runs until all 32 are running, so they are in
	// flight together. A read answered without running (refused) lets the
	// others go, and the case fails on its code.
	var arrivals sync.WaitGroup
	arrivals.Add(together)
	allIn, answeredEarly := make(chan struct{}), make(chan struct{})
	var answered sync.Once
	go func() { arrivals.Wait(); close(allIn) }()
	bound := testwait.Context(t)
	read := func(id string) Operation {
		return Operation{ID: id, Summary: id, Fields: map[string]Field{}, Run: func(context.Context, Params) Outcome {
			arrivals.Done()
			select {
			case <-allIn:
			case <-answeredEarly:
			case <-bound.Done():
			}
			// Read again: once all are in, the first to answer also closes
			// answeredEarly, and a read woken late may find both ready.
			select {
			case <-allIn:
				return Outcome{Complete: true, Value: map[string]string{"read": id}}
			default:
				return Outcome{Error: &Failure{Code: "not_in_flight_together", Message: "the 32 reads were not all running at once"}}
			}
		}}
	}
	c, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: []Operation{read("fleet.get"), read("overview.get"), read("runtime.get"), read("object.list")}})
	if err != nil {
		t.Fatal(err)
	}
	var done sync.WaitGroup
	codes := make([]int, together)
	for i := range codes {
		done.Add(1)
		go func() {
			defer done.Done()
			defer answered.Do(func() { close(answeredEarly) })
			codes[i], _ = frontRead(t, c, []string{"fleet.get", "object.list", "overview.get", "runtime.get"}[i%4])
		}()
	}
	done.Wait()
	for i, code := range codes {
		if code != 200 {
			t.Errorf("front read %d answered %d, want every one answered", i, code)
		}
	}
}

// The front read takes the administrator key, nothing else.
func TestTheFrontReadTakesTheAdministratorKey(t *testing.T) {
	c, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: []Operation{{ID: "fleet.get", Summary: "fleet", Fields: map[string]Field{}, Run: func(context.Context, Params) Outcome { return Outcome{Complete: true} }}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"channel_version": Version, "mode": "invoke", "operation": "fleet.get", "params": map[string]any{}})
	w := httptest.NewRecorder()
	c.FrontReadHandler(func(*http.Request) error { return errors.New("not the key") }).ServeHTTP(w, httptest.NewRequest(http.MethodPost, cliauth.FrontReadPath, bytes.NewReader(raw)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("front read without the key = %d %s", w.Code, w.Body.String())
	}
}

type namedSessionAuth struct{}

func (namedSessionAuth) Authenticate(context.Context, string) (cliauth.Session, error) {
	return cliauth.Session{ID: "named-session", EnvironmentID: "test", Scope: cliauth.ScopeReadonly, ExpiresAt: time.Now().Add(time.Hour),
		Operator: "ops-a", IssuedVia: cliauth.IssuedViaFront, FallbackBaseURLs: []string{"https://ops.example/own-alarmd/"}}, nil
}

func (namedSessionAuth) Admit(_ context.Context, session cliauth.Session, _ bool) (cliauth.Session, error) {
	return session, nil
}

// An SRE action names the operator the session was issued to and the route
// that issued it.
func TestAnSREReceiptNamesTheOperator(t *testing.T) {
	controller := true
	client := &postObservationClient{target: corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "web", UID: "workload-uid", Controller: &controller}}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "web:v1"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "web", ContainerID: "containerd://fixture", ImageID: "sha256:fixture", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}},
	}}
	provider, err := pod.New(pod.Options{Client: client, Manager: &auditManager{}, Executor: &postObservationExecutor{}, DefaultNamespace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var actions []SREAction
	c, err := New(Options{Auth: namedSessionAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: SREOperations(SREOptions{Pods: provider, Audit: func(action SREAction) {
		mu.Lock()
		defer mu.Unlock()
		actions = append(actions, action)
	}})})
	if err != nil {
		t.Fatal(err)
	}
	_, response := call(t, c, envelope(c, "invoke", pod.OpDeletePod, Params{"target": map[string]any{"namespace": "fixture", "pod": "web-0", "uid": "pod-uid"}}))
	if response.Error != nil {
		t.Fatalf("delete: %+v", response)
	}
	// Every answer tells the session its fallback entry, so a session kept
	// alive in-band learns it without a new login.
	if response.Meta.Session == nil || len(response.Meta.Session.FallbackBaseURLs) != 1 || response.Meta.Session.FallbackBaseURLs[0] != "https://ops.example/own-alarmd/" {
		t.Fatalf("meta.session = %+v, want the session's fallback entry", response.Meta.Session)
	}
	if len(actions) != 1 || actions[0].Operator != "ops-a" || actions[0].IssuedVia != cliauth.IssuedViaFront || actions[0].SessionID != "named-session" {
		t.Fatalf("actions %+v, want one naming ops-a issued through the front surface", actions)
	}
	// The receipt also names the entry the call came in on when it was not
	// the session's: alarmd's own address, which the CLI falls back to when
	// the admin cannot reach alarmd. Any other value is no entry.
	for _, tc := range []struct{ header, want string }{{EntryAlarmd, EntryAlarmd}, {"admin", ""}, {"", ""}} {
		raw, _ := json.Marshal(envelope(c, "invoke", pod.OpDeletePod, Params{"target": map[string]any{"namespace": "fixture", "pod": "web-0", "uid": "pod-uid"}}))
		r := httptest.NewRequest("POST", "/api/cli/channel", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer test-only")
		if tc.header != "" {
			r.Header.Set(EntryHeader, tc.header)
		}
		c.ServeHTTP(httptest.NewRecorder(), r)
		mu.Lock()
		last := actions[len(actions)-1]
		mu.Unlock()
		if last.Entry != tc.want {
			t.Errorf("header %q: receipt entry %q, want %q", tc.header, last.Entry, tc.want)
		}
	}
	if len(actions) != 4 {
		t.Fatalf("recorded %d actions, want 4", len(actions))
	}
}

// The drawer reads a strategy's source configuration through the front, and
// only that view: the published view, and a call that names no view, are
// refused before the operation runs.
func TestTheFrontReadsAStrategyConfigByItsSourceViewOnly(t *testing.T) {
	ran := 0
	c, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: []Operation{{ID: "strategy.config", Summary: "config",
			Fields: map[string]Field{"view": {Type: "string", Enum: []string{"source", "published"}}, "strategy_id": {Type: "string"}},
			Run: func(context.Context, Params) Outcome {
				ran++
				return Outcome{Complete: true, Value: obevidence.Result{Status: "ok"}}
			}}}})
	if err != nil {
		t.Fatal(err)
	}
	read := func(params map[string]any) int {
		raw, _ := json.Marshal(map[string]any{"channel_version": Version, "mode": "invoke", "operation": "strategy.config", "params": params})
		w := httptest.NewRecorder()
		c.FrontReadHandler(func(*http.Request) error { return nil }).ServeHTTP(w, httptest.NewRequest(http.MethodPost, cliauth.FrontReadPath, bytes.NewReader(raw)))
		return w.Code
	}
	if code := read(map[string]any{"view": "source", "strategy_id": "80"}); code != http.StatusOK || ran != 1 {
		t.Fatalf("source view answered %d, ran %d; want it read", code, ran)
	}
	for _, params := range []map[string]any{{"view": "published", "strategy_id": "80"}, {"strategy_id": "80"}} {
		if code := read(params); code != http.StatusForbidden {
			t.Errorf("%v answered %d, want refused for the front", params, code)
		}
	}
	if ran != 1 {
		t.Fatalf("a refused view still ran the operation (%d runs)", ran)
	}
}

// A front read of a strategy's config does not carry where alarmd read it
// from: the store's address, database, prefix and key, and a failed read's
// own error text, are the cluster's. The value and its omissions are kept.
func TestTheFrontAnswerOfAStrategyConfigLeavesOutTheStoreLocation(t *testing.T) {
	result := obevidence.Result{Status: "ok", Source: "source_cache", Complete: true,
		Location:   obevidence.Location{Role: "source", Address: "store.example:6379", DB: 3, Prefix: "cache", Key: "cache.strategy.80"},
		Value:      map[string]any{"id": 80},
		Omitted:    []obevidence.Omission{{Path: "$.runtime_config", Reason: "value_shape_only"}},
		ReasonText: "dial tcp store.example:6379: refused"}
	c, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: []Operation{{ID: "strategy.config", Summary: "config",
			Fields: map[string]Field{"view": {Type: "string"}, "strategy_id": {Type: "string"}},
			Run:    func(context.Context, Params) Outcome { return Outcome{Complete: true, Value: result} }}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"channel_version": Version, "mode": "invoke", "operation": "strategy.config", "params": map[string]any{"view": "source", "strategy_id": "80"}})
	w := httptest.NewRecorder()
	c.FrontReadHandler(func(*http.Request) error { return nil }).ServeHTTP(w, httptest.NewRequest(http.MethodPost, cliauth.FrontReadPath, bytes.NewReader(raw)))
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("front read answered %d %s", w.Code, body)
	}
	for _, private := range []string{"store.example", "cache.strategy.80", `"db":3`, `"prefix":"cache"`, "refused"} {
		if strings.Contains(body, private) {
			t.Errorf("the front answer carries %q: %s", private, body)
		}
	}
	for _, kept := range []string{`"id":80`, "$.runtime_config", "value_shape_only", `"role":"source"`} {
		if !strings.Contains(body, kept) {
			t.Errorf("the front answer lost %q: %s", kept, body)
		}
	}
}

// The real strategy.config, through the front: what it read and from where
// comes back without the store's address, database, prefix and key.
func TestTheRealStrategyConfigReadThroughTheFrontLeavesOutTheStore(t *testing.T) {
	client := redistest.Start(t).Client()
	service := obevidence.New(obevidence.Options{SourceStrategy: obevidence.RedisBinding{Client: client,
		Location: obevidence.Location{Role: "source", Address: "store.example:6379", DB: 3, Prefix: "cache"}}})
	var config Operation
	for _, op := range StoreOperations(service) {
		if op.ID == "strategy.config" {
			config = op
		}
	}
	if config.ID == "" {
		t.Fatal("strategy.config is not among the store operations")
	}
	c, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: []Operation{config}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"channel_version": Version, "mode": "invoke", "operation": "strategy.config", "params": map[string]any{"view": "source", "strategy_id": "80"}})
	w := httptest.NewRecorder()
	c.FrontReadHandler(func(*http.Request) error { return nil }).ServeHTTP(w, httptest.NewRequest(http.MethodPost, cliauth.FrontReadPath, bytes.NewReader(raw)))
	body := w.Body.String()
	var answer struct {
		Result struct {
			Status   string         `json:"status"`
			Location map[string]any `json:"location"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil || answer.Result.Status == "" {
		t.Fatalf("front read answered %d %s, want the config read's status", w.Code, body)
	}
	for _, private := range []string{"store.example", `"db":3`, `"prefix":"cache"`, "cache."} {
		if strings.Contains(body, private) {
			t.Errorf("the front answer carries %q: %s", private, body)
		}
	}
	if answer.Result.Location["role"] != "source" {
		t.Errorf("location %v, want the store's role kept", answer.Result.Location)
	}
}

// A strategy.config answer of a shape the trim does not know is withheld,
// not passed through: a change to the result can only fail closed.
func TestAStrategyConfigAnswerTheFrontCannotShapeIsWithheld(t *testing.T) {
	c, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: []Operation{{ID: "strategy.config", Summary: "config",
			Fields: map[string]Field{"view": {Type: "string"}, "strategy_id": {Type: "string"}},
			Run: func(context.Context, Params) Outcome {
				return Outcome{Complete: true, Value: map[string]any{"location": map[string]any{"address": "store.example:6379"}}}
			}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"channel_version": Version, "mode": "invoke", "operation": "strategy.config", "params": map[string]any{"view": "source", "strategy_id": "80"}})
	w := httptest.NewRecorder()
	c.FrontReadHandler(func(*http.Request) error { return nil }).ServeHTTP(w, httptest.NewRequest(http.MethodPost, cliauth.FrontReadPath, bytes.NewReader(raw)))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "front_answer_unshaped") || strings.Contains(w.Body.String(), "store.example") {
		t.Fatalf("an unshaped answer = %d %s, want 502 front_answer_unshaped without the address", w.Code, w.Body.String())
	}
}
