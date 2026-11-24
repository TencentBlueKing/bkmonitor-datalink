// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"linkd/internal/pluginruntime"
)

type executorFunc func(context.Context, pluginruntime.Request) (pluginruntime.Result, error)

func (f executorFunc) Execute(ctx context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
	return f(ctx, r)
}

type publisherFunc func(context.Context, pluginruntime.Request, pluginruntime.Result) error

func (f publisherFunc) Publish(ctx context.Context, r pluginruntime.Request, v pluginruntime.Result) error {
	return f(ctx, r, v)
}

type resultFixture struct {
	result           pluginruntime.Result
	found            bool
	saveError        error
	completed        bool
	completeError    error
	onComplete       func(context.Context, pluginruntime.Request, pluginruntime.Result) error
	request          *pluginruntime.Request
	requestError     error
	attempts         int
	attemptError     error
	publications     int
	publicationError error
}

func (s *resultFixture) BeginPublication(context.Context, pluginruntime.Request) (int, error) {
	if s.publicationError != nil {
		return 0, s.publicationError
	}
	if s.publications >= 3 {
		return 0, nil
	}
	s.publications++
	return s.publications, nil
}

func (s *resultFixture) BeginAttempt(context.Context, pluginruntime.Request) (bool, error) {
	if s.attemptError != nil {
		return false, s.attemptError
	}
	if s.attempts >= 3 {
		return false, nil
	}
	s.attempts++
	return true, nil
}

func (s *resultFixture) SaveRequest(_ context.Context, request pluginruntime.Request) (pluginruntime.Request, error) {
	if s.requestError != nil {
		return request, s.requestError
	}
	if s.request == nil {
		s.request = &request
	}
	return *s.request, nil
}

func (s *resultFixture) LoadResult(context.Context, pluginruntime.Request) (*StoredResult, error) {
	if !s.found {
		return nil, nil
	}
	return &StoredResult{Result: s.result, Completed: s.completed}, nil
}

func (s *resultFixture) Complete(ctx context.Context, request pluginruntime.Request, result pluginruntime.Result) error {
	if s.completeError != nil {
		return s.completeError
	}
	if s.onComplete != nil {
		if err := s.onComplete(ctx, request, result); err != nil {
			return err
		}
	}
	s.result, s.found, s.completed = result, true, true
	return nil
}

func (s *resultFixture) SaveResult(_ context.Context, _ pluginruntime.Request, r pluginruntime.Result) (pluginruntime.Result, error) {
	if s.saveError != nil {
		return r, s.saveError
	}
	if !s.found {
		s.result = r
		s.found = true
	}
	return s.result, nil
}

func TestFailedPublicationReusesComputedBatch(t *testing.T) {
	calls, publishes := 0, 0
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		calls++
		return pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{"fingerprint":"a"}`)}}, nil
	}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
		publishes++
		if publishes == 1 {
			return errors.New("ACK lost")
		}
		return nil
	}), nil)
	service.results = &resultFixture{}
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); err == nil {
		t.Fatal("lost ACK reported success")
	}
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || publishes != 2 {
		t.Fatalf("unexpected replay: executions=%d publishes=%d", calls, publishes)
	}
}

func TestComputationPersistenceFailurePreventsPublication(t *testing.T) {
	failure := errors.New("result store unavailable")
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{}`)}}, nil
	}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
		t.Fatal("published before result persistence")
		return nil
	}), nil)
	service.results = &resultFixture{saveError: failure}
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); !errors.Is(err, failure) {
		t.Fatalf("result store failure hidden: %v", err)
	}
}

func TestExecutionPublicationProgress(t *testing.T) {
	failure := errors.New("dependency unavailable")
	for _, tc := range []struct {
		name   string
		failAt string
		empty  bool
		want   []string
	}{
		{"success", "", false, []string{"execute", "publish", "commit"}},
		{"empty pull", "", true, []string{"execute", "commit"}},
		{"plugin fails", "execute", false, []string{"execute"}},
		{"publication fails", "publish", false, []string{"execute", "publish"}},
		{"progress fails", "commit", false, []string{"execute", "publish", "commit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			step := func(name string) error {
				calls = append(calls, name)
				if name == tc.failAt {
					return failure
				}
				return nil
			}
			request := pluginruntime.Request{TenantID: "tenant", SourceID: "source", RequestID: "request"}
			service := New(executorFunc(func(ctx context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
				result := pluginruntime.Result{ProposedState: map[string]any{"meta_info": 123}}
				if !tc.empty {
					result.Events = []json.RawMessage{json.RawMessage(`{"fingerprint":"a"}`)}
				}
				return result, step("execute")
			}), publisherFunc(func(ctx context.Context, r pluginruntime.Request, result pluginruntime.Result) error {
				return step("publish")
			}), &resultFixture{onComplete: func(ctx context.Context, r pluginruntime.Request, result pluginruntime.Result) error {
				if r.TenantID != request.TenantID || r.SourceID != request.SourceID || result.ProposedState["meta_info"] != 123 {
					t.Fatal("progress scope lost")
				}
				return step("commit")
			}})
			_, err := service.Run(t.Context(), request)
			if tc.failAt != "" && !errors.Is(err, failure) {
				t.Fatalf("error chain lost: %v", err)
			}
			if tc.failAt == "" && err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("calls %v, want %v", calls, tc.want)
			}
		})
	}
}

func TestExplicitTemporaryExecutionRetries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      string
		succeedAt int
		asError   bool
		cancel    bool
		wantCalls int
	}{
		{"temporary then success", "temporary_error", 3, false, false, 3},
		{"wrapped temporary then success", "temporary_error", 2, true, false, 2},
		{"exhausted", "temporary_error", 0, true, false, 3},
		{"permanent", "plugin_error", 0, false, false, 1},
		{"transport unknown", "", 0, true, false, 1},
		{"cancelled between attempts", "temporary_error", 0, true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls, publishes := 0, 0
			request := pluginruntime.Request{RequestID: "fixed", TenantID: "tenant", SourceID: "source", ReceivedAt: "2026-11-14T00:00:00Z"}
			service := New(executorFunc(func(_ context.Context, actual pluginruntime.Request) (pluginruntime.Result, error) {
				calls++
				if !reflect.DeepEqual(actual, request) {
					t.Fatal("retry changed request")
				}
				if calls == tc.succeedAt {
					return pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{}`)}}, nil
				}
				if tc.cancel {
					cancel()
				}
				if tc.code == "" {
					return pluginruntime.Result{}, errors.New("connection lost")
				}
				failure := &pluginruntime.Failure{Code: tc.code, Type: "TemporaryError"}
				if tc.asError {
					return pluginruntime.Result{}, fmt.Errorf("executor: %w", failure)
				}
				return pluginruntime.Result{Failure: failure}, nil
			}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
				publishes++
				return nil
			}), nil)
			_, err := service.Run(ctx, request)
			if calls != tc.wantCalls {
				t.Fatalf("calls=%d want=%d", calls, tc.wantCalls)
			}
			if tc.succeedAt > 0 {
				if err != nil || publishes != 1 {
					t.Fatalf("success: publishes=%d err=%v", publishes, err)
				}
			} else if err == nil || publishes != 0 {
				t.Fatalf("failure published: publishes=%d err=%v", publishes, err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestCompletedRequestDoesNotRepeatSideEffects(t *testing.T) {
	calls, publishes, commits := 0, 0, 0
	store := &resultFixture{onComplete: func(context.Context, pluginruntime.Request, pluginruntime.Result) error { commits++; return nil }}
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		calls++
		return pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{"fingerprint":"a"}`)}, ProposedState: map[string]any{"cursor": "first"}}, nil
	}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
		publishes++
		return nil
	}), store)
	first, err := service.Run(t.Context(), pluginruntime.Request{})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟后续周期已经推进游标；再次读取旧请求不能重新提交旧游标。
	again, err := service.Run(t.Context(), pluginruntime.Request{})
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("completed result changed: %v", err)
	}
	if calls != 1 || publishes != 1 || commits != 1 {
		t.Fatalf("repeated side effects: %d %d %d", calls, publishes, commits)
	}
}

func TestCompletionFailureReturnsErrorAndRetainsComputation(t *testing.T) {
	failure := errors.New("completion store unavailable")
	store := &resultFixture{completeError: failure}
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{}, nil
	}), nil, nil)
	service.results = store
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); !errors.Is(err, failure) {
		t.Fatalf("completion failure hidden: %v", err)
	}
	if !store.found || store.completed {
		t.Fatal("failed completion discarded result or reported completion")
	}
}

// cancelResultStore 模拟存储适配器忽略取消并在返回成功前撤销请求。
type cancelResultStore struct {
	*resultFixture
	afterLoad func()
	afterSave func()
}

func (s *cancelResultStore) LoadResult(ctx context.Context, request pluginruntime.Request) (*StoredResult, error) {
	result, err := s.resultFixture.LoadResult(ctx, request)
	if s.afterLoad != nil {
		s.afterLoad()
	}
	return result, err
}

func (s *cancelResultStore) SaveResult(ctx context.Context, request pluginruntime.Request, result pluginruntime.Result) (pluginruntime.Result, error) {
	stored, err := s.resultFixture.SaveResult(ctx, request, result)
	if s.afterSave != nil {
		s.afterSave()
	}
	return stored, err
}

func TestCancellationStopsNextCollectionStage(t *testing.T) {
	for _, phase := range []string{"before run", "cached result", "execute", "save result", "publish"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{"fingerprint":"a"}`)}, ProposedState: map[string]any{"cursor": "next"}}
			store := &cancelResultStore{resultFixture: &resultFixture{}}
			switch phase {
			case "before run":
				cancel()
			case "cached result":
				store.found, store.result, store.afterLoad = true, result, cancel
			case "save result":
				store.afterSave = cancel
			}
			executions, publications := 0, 0
			service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
				executions++
				if phase == "execute" {
					cancel()
				}
				return result, nil
			}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
				publications++
				if phase == "publish" {
					cancel()
				}
				return nil
			}), store)
			if _, err := service.Run(ctx, pluginruntime.Request{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if store.completed {
				t.Fatal("committed cursor after revocation")
			}
			if phase != "publish" && publications != 0 {
				t.Fatal("published after revocation")
			}
			if (phase == "before run" || phase == "cached result") && executions != 0 {
				t.Fatal("executed after revocation")
			}
			if phase == "execute" && store.found {
				t.Fatal("persisted computation after revocation")
			}
		})
	}
}

func TestExecutionInputFailurePreventsPluginCall(t *testing.T) {
	failure := errors.New("request store unavailable")
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		t.Fatal("executed before input persistence")
		return pluginruntime.Result{}, nil
	}), nil, &resultFixture{requestError: failure})
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); !errors.Is(err, failure) {
		t.Fatalf("input storage cause lost: %v", err)
	}
}

func TestFailedExecutionReusesFirstInput(t *testing.T) {
	first := pluginruntime.Request{RequestID: "pull/2026-11-14T12:00:00Z", TenantID: "tenant", SourceID: "source", SourceVersion: 1, ReceivedAt: "2026-11-14T12:00:00Z", State: map[string]any{"meta_info": "first"}}
	calls := 0
	service := New(executorFunc(func(_ context.Context, request pluginruntime.Request) (pluginruntime.Result, error) {
		calls++
		if !reflect.DeepEqual(request, first) {
			t.Fatal("replay used new execution facts")
		}
		if calls == 1 {
			return pluginruntime.Result{}, errors.New("execution failed")
		}
		return pluginruntime.Result{}, nil
	}), nil, &resultFixture{})
	if _, err := service.Run(t.Context(), first); err == nil {
		t.Fatal("failure hidden")
	}
	changed := first
	changed.State = map[string]any{"meta_info": "changed"}
	changed.ReceivedAt = "2026-11-14T13:00:00Z"
	if _, err := service.Run(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("executions=%d", calls)
	}
}

func TestExplicitPluginFailureIsTerminal(t *testing.T) {
	for _, code := range []string{"plugin_error", "temporary_error"} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			failure := &pluginruntime.Failure{Code: code, Type: "PluginException"}
			store := &resultFixture{}
			service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
				calls++
				return pluginruntime.Result{Failure: failure, ProposedState: map[string]any{"cursor": "partial"}, Events: []json.RawMessage{json.RawMessage(`{}`)}}, nil
			}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
				t.Fatal("published failed computation")
				return nil
			}), store)
			request := pluginruntime.Request{RequestID: "fixed", TenantID: "tenant", SourceID: "source"}
			for range 2 {
				result, err := service.Run(t.Context(), request)
				if !errors.Is(err, failure) || result.Failure != failure {
					t.Fatalf("terminal cause lost: %+v %v", result, err)
				}
			}
			wantCalls := 1
			if code == "temporary_error" {
				wantCalls = 3
			}
			if calls != wantCalls || !store.completed {
				t.Fatalf("terminal replay reexecuted: calls=%d completed=%v", calls, store.completed)
			}
			if store.result.ProposedState != nil || len(store.result.Events) != 0 {
				t.Fatal("terminal failure retained partial output")
			}
		})
	}
}

func TestFailureFinalizationErrorKeepsBothCauses(t *testing.T) {
	pluginFailure := &pluginruntime.Failure{Code: "plugin_error", Type: "ValueError"}
	storageFailure := errors.New("terminal store unavailable")
	store := &resultFixture{completeError: storageFailure}
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{}, pluginFailure
	}), nil, store)
	_, err := service.Run(t.Context(), pluginruntime.Request{})
	if !errors.Is(err, pluginFailure) || !errors.Is(err, storageFailure) || store.completed {
		t.Fatalf("failure finalization: %v completed=%v", err, store.completed)
	}
}

func TestUnknownExecutionOutcomeRemainsPending(t *testing.T) {
	store := &resultFixture{}
	failure := errors.New("connection lost")
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{}, failure
	}), nil, store)
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); !errors.Is(err, failure) || store.completed {
		t.Fatalf("unknown execution outcome terminated: %v completed=%v", err, store.completed)
	}
}

func TestAttemptBudgetSurvivesServiceRecreation(t *testing.T) {
	store := &resultFixture{}
	calls := 0
	unknown := errors.New("execution response lost")
	for attempt := 1; attempt <= 4; attempt++ {
		service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
			calls++
			return pluginruntime.Result{}, unknown
		}), nil, store)
		_, err := service.Run(t.Context(), pluginruntime.Request{RequestID: "fixed"})
		if attempt <= 3 {
			if !errors.Is(err, unknown) || store.completed {
				t.Fatalf("unknown execution prematurely terminated: %v", err)
			}
		} else {
			var failure *pluginruntime.Failure
			if !errors.As(err, &failure) || failure.Code != "attempts_exhausted" || !store.completed {
				t.Fatalf("budget not terminal: %v", err)
			}
		}
	}
	if calls != 3 || store.attempts != 3 {
		t.Fatalf("executions=%d reservations=%d", calls, store.attempts)
	}
}

func TestAttemptReservationFailurePreventsExecution(t *testing.T) {
	failure := errors.New("attempt store unavailable")
	store := &resultFixture{attemptError: failure}
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		t.Fatal("executed without durable attempt reservation")
		return pluginruntime.Result{}, nil
	}), nil, store)
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); !errors.Is(err, failure) || store.completed {
		t.Fatalf("reservation error lost: %v", err)
	}
}

func TestPublicationBudgetSurvivesServiceRecreation(t *testing.T) {
	store := &resultFixture{}
	executions, publications := 0, 0
	cause := errors.New("publication reply lost")
	for attempt := 1; attempt <= 4; attempt++ {
		service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
			executions++
			return pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{"fingerprint":"a"}`)}, ProposedState: map[string]any{"cursor": "uncommitted"}}, nil
		}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error { publications++; return cause }), store)
		_, err := service.Run(t.Context(), pluginruntime.Request{RequestID: "same"})
		if attempt <= 3 && !errors.Is(err, cause) {
			t.Fatalf("publication cause lost: %v", err)
		}
		if attempt >= 3 && !store.completed {
			t.Fatal("exhausted publication not terminated")
		}
	}
	if executions != 1 || publications != 3 || store.result.ProposedState != nil {
		t.Fatalf("executions=%d publications=%d state=%v", executions, publications, store.result.ProposedState)
	}
}

func TestPermanentPublicationFailureStopsWithoutRetry(t *testing.T) {
	cause := errors.New("invalid message")
	store := &resultFixture{}
	calls := 0
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{}`)}}, nil
	}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
		calls++
		return &permanentPublicationError{cause}
	}), store)
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); err == nil {
		t.Fatal("terminal failure returned success")
	}
	if calls != 1 || !store.completed {
		t.Fatal("permanent error was retried")
	}
}

func TestPublicationReservationFailurePreventsSending(t *testing.T) {
	cause := errors.New("attempt store unavailable")
	store := &resultFixture{publicationError: cause}
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{}`)}}, nil
	}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
		t.Fatal("sent without reserved attempt")
		return nil
	}), store)
	if _, err := service.Run(t.Context(), pluginruntime.Request{}); !errors.Is(err, cause) || store.completed {
		t.Fatalf("reservation failure: %v", err)
	}
}
