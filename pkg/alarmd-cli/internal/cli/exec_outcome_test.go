// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type brokenExecBody struct{}

func (brokenExecBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenExecBody) Close() error             { return nil }

func TestDispatchedExecFailuresSaveUnknownOutcomeWithoutRetry(t *testing.T) {
	for _, name := range []string{"timeout", "EOF", "body read", "oversize", "invalid JSON", "invalid envelope", "missing receipt", "missing remote state", "missing exit code", "gateway", "server response budget"} {
		t.Run(name, func(t *testing.T) {
			p := fixtureProfile("http://fixture.test")
			p.Scope, p.RefreshToken = sessionScope, "refresh-fixture-secret"
			store := Store{t.TempDir()}
			_ = store.save(p, false)
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return jsonHTTP(envelope(p, "ok", map[string]any{"effect": "exec", "required_scope": sessionScope}), 200), nil
				}
				valid := envelope(p, "ok", map[string]any{"remote_state": "remote_completed", "exit_code": 0, "password": "sensitive-fixture", "stdout": p.AccessToken})
				switch name {
				case "timeout":
					return nil, context.DeadlineExceeded
				case "EOF":
					return nil, io.EOF
				case "body read":
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: brokenExecBody{}}, nil
				case "oversize":
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponse+1)))}, nil
				case "invalid JSON":
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"password":"sensitive-fixture"`))}, nil
				case "invalid envelope":
					objectField(valid, "meta")["environment_id"] = "different-environment"
				case "missing receipt":
					valid["result"] = nil
				case "missing remote state":
					delete(objectField(valid, "result"), "remote_state")
				case "missing exit code":
					delete(objectField(valid, "result"), "exit_code")
				case "gateway":
					return jsonHTTP(map[string]any{"gateway": "please retry", "password": "sensitive-fixture"}, 502), nil
				case "server response budget":
					valid = envelope(p, "error", nil)
					valid["error"] = map[string]any{"code": "response_budget_exceeded", "message": "receipt too large"}
					return jsonHTTP(valid, 502), nil
				}
				return jsonHTTP(valid, 200), nil
			})}
			code, out, _, _ := run(t, store, client, "", "invoke", "pod.exec", "--env", p.EnvironmentID, "--input", `{"target":{"pod":"fixture"},"argv":["sh"],"stdin":"private-script-fixture"}`)
			if code != 1 || calls != 2 || stringField(objectField(out, "error"), "code") != "remote_state_unknown" || objectField(out, "evidence")["complete"] != false || !strings.Contains(stringField(out, "summary"), "Inspect") {
				t.Fatalf("lost execution uncertainty: exit=%d calls=%d out=%+v", code, calls, out)
			}
			result := objectField(out, "result")
			if stringField(result, "remote_state") != "remote_state_unknown" || !strings.HasPrefix(stringField(result, "request_digest"), "sha256:") || objectField(result, "target")["pod"] != "fixture" {
				t.Fatalf("lost request context: %+v", result)
			}
			data, err := os.ReadFile(stringField(objectField(out, "meta"), "result_file"))
			if err != nil || !json.Valid(data) {
				t.Fatalf("unknown outcome not saved: %s %v", data, err)
			}
			for _, secret := range []string{"sensitive-fixture", p.AccessToken, p.RefreshToken, "private-script-fixture"} {
				if strings.Contains(string(data), secret) {
					t.Fatalf("saved sensitive input or response: %s", secret)
				}
			}
			if name == "invalid envelope" && objectField(result, "unverified_response") == nil {
				t.Fatal("decoded malformed receipt was discarded")
			}
		})
	}
}

func TestExecDisconnectAfterActualServerAdmissionIsUnknown(t *testing.T) {
	var p Profile
	var invokes atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["mode"] == "describe" {
			writeJSON(t, w, envelope(p, "ok", map[string]any{"effect": "exec", "required_scope": sessionScope}))
			return
		}
		invokes.Add(1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer s.Close()
	p = fixtureProfile(s.URL)
	p.Scope = sessionScope
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	code, out, _, _ := run(t, store, s.Client(), "", "invoke", "pod.exec", "--env", p.EnvironmentID)
	if code != 1 || invokes.Load() != 1 || stringField(objectField(out, "error"), "code") != "remote_state_unknown" || stringField(objectField(out, "meta"), "result_file") == "" {
		t.Fatalf("dispatched exec lost: exit=%d invokes=%d result=%+v", code, invokes.Load(), out)
	}
}

func TestExecPreAdmissionRejectionsRetainTheirErrors(t *testing.T) {
	for _, name := range []string{"describe failure", "permission", "connection refused", "server denial", "provider unavailable", "auth store unavailable"} {
		t.Run(name, func(t *testing.T) {
			p := fixtureProfile("http://fixture.test")
			p.Scope = sessionScope
			required := sessionScope
			if name == "permission" {
				// An operation requiring a scope this CLI does not hold.
				required = "deployment_ops_admin"
			}
			store := Store{t.TempDir()}
			_ = store.save(p, false)
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if name == "describe failure" {
					return nil, errors.New("unavailable")
				}
				if calls == 1 {
					return jsonHTTP(envelope(p, "ok", map[string]any{"effect": "exec", "required_scope": required}), 200), nil
				}
				if name == "connection refused" {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
				}
				out := envelope(p, "error", nil)
				if name == "provider unavailable" || name == "auth store unavailable" {
					code := "operation_unavailable"
					if name == "auth store unavailable" {
						code = "auth_store_unavailable"
					}
					out["error"] = map[string]any{"code": code, "message": "not admitted"}
					return jsonHTTP(out, 503), nil
				}
				out["error"] = map[string]any{"code": "permission_denied", "message": "scope denied"}
				return jsonHTTP(out, 403), nil
			})}
			code, out, _, _ := run(t, store, client, "", "invoke", "pod.exec", "--env", p.EnvironmentID)
			want := map[string]string{"describe failure": "protocol_error", "permission": "permission_denied", "connection refused": "request_failed", "server denial": "permission_denied", "provider unavailable": "operation_unavailable", "auth store unavailable": "auth_store_unavailable"}[name]
			if code != 1 || stringField(objectField(out, "error"), "code") != want {
				t.Fatalf("pre-execution error changed: %+v", out)
			}
			if (name == "describe failure" || name == "permission") && calls != 1 {
				t.Fatal("pre-admission rejection invoked operation")
			}
		})
	}
}

func TestDescribeDeadlineIncludesAdmissionAndTransport(t *testing.T) {
	p := fixtureProfile("http://fixture.test")
	p.Scope = sessionScope
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonHTTP(envelope(p, "ok", map[string]any{"effect": "exec", "required_scope": sessionScope, "execution_timeout_ms": 20, "request_limits": map[string]any{"admission_timeout_ms": 30, "transport_margin_ms": 20}}), 200), nil
		}
		deadline, ok := r.Context().Deadline()
		if remaining := time.Until(deadline); !ok || remaining <= 60*time.Millisecond || remaining > 70*time.Millisecond {
			t.Fatalf("deadline omitted admission or margin: %v %s", ok, remaining)
		}
		select {
		case <-time.After(45 * time.Millisecond):
			return jsonHTTP(envelope(p, "ok", map[string]any{"remote_state": "remote_completed", "exit_code": 0}), 200), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	if code, out, _, _ := run(t, store, client, "", "invoke", "pod.exec", "--env", p.EnvironmentID); code != 0 || calls != 2 {
		t.Fatalf("admission consumed execution deadline: %+v", out)
	}
}

func TestInvalidAdmissionAndMarginAreRejectedBeforeInvoke(t *testing.T) {
	for _, limits := range []map[string]any{{"admission_timeout_ms": 0}, {"admission_timeout_ms": "3000"}, {"transport_margin_ms": -1}, {"transport_margin_ms": []any{5000}}} {
		p := fixtureProfile("http://fixture.test")
		store := Store{t.TempDir()}
		_ = store.save(p, false)
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return jsonHTTP(envelope(p, "ok", map[string]any{"execution_timeout_ms": 35000, "request_limits": limits}), 200), nil
		})}
		code, out, _, _ := run(t, store, client, "", "invoke", "fixture.read", "--env", p.EnvironmentID)
		if code != 1 || calls != 1 || stringField(objectField(out, "error"), "code") != "protocol_error" {
			t.Fatalf("invalid budget was invoked: limits=%+v out=%+v calls=%d", limits, out, calls)
		}
	}
}

// An unsupervised run is a receipt: with its exit code, or cut at its
// deadline. One with neither is not, and is saved as unknown.
func TestAnUnsupervisedExecReceiptNeedsAnEnd(t *testing.T) {
	for _, c := range []struct {
		name    string
		receipt map[string]any
		code    int
	}{
		{"exit code", map[string]any{"remote_state": "remote_unsupervised", "unsupervised_reason": "no_shell", "exit_code": 5}, 0},
		{"cut", map[string]any{"remote_state": "remote_unsupervised", "unsupervised_reason": "no_shell", "stream_cut": true}, 0},
		{"neither", map[string]any{"remote_state": "remote_unsupervised", "unsupervised_reason": "no_shell"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := fixtureProfile("http://fixture.test")
			p.Scope = sessionScope
			store := Store{t.TempDir()}
			_ = store.save(p, false)
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return jsonHTTP(envelope(p, "ok", map[string]any{"effect": "exec", "required_scope": sessionScope}), 200), nil
				}
				return jsonHTTP(envelope(p, "ok", c.receipt), 200), nil
			})}
			code, out, _, _ := run(t, store, client, "", "invoke", "pod.exec", "--env", p.EnvironmentID)
			if code != c.code || calls != 2 {
				t.Fatalf("exit %d after %d calls, want %d: %+v", code, calls, c.code, out)
			}
			if c.code == 1 && stringField(objectField(out, "error"), "code") != "remote_state_unknown" {
				t.Fatalf("a receipt without an end was not unknown: %+v", out)
			}
		})
	}
}

// A remote_state this client does not know - one a newer server reports -
// is shown as the server wrote it.
func TestAnUnknownRemoteStateIsShownRaw(t *testing.T) {
	p := fixtureProfile("http://fixture.test")
	p.Scope = sessionScope
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonHTTP(envelope(p, "ok", map[string]any{"effect": "exec", "required_scope": sessionScope}), 200), nil
		}
		return jsonHTTP(envelope(p, "ok", map[string]any{"remote_state": "remote_some_future_state", "detail": "kept"}), 200), nil
	})}
	code, out, _, _ := run(t, store, client, "", "invoke", "pod.exec", "--env", p.EnvironmentID)
	if code != 0 || calls != 2 || stringField(objectField(out, "result"), "remote_state") != "remote_some_future_state" {
		t.Fatalf("exit %d after %d calls: %+v", code, calls, out)
	}
}

// A malformed execution timeout is refused before the invoke, as the
// request budgets are: a larger one is a newer server, a zero, negative or
// non-number one is no server's.
func TestAMalformedExecutionTimeoutIsRejectedBeforeInvoke(t *testing.T) {
	for _, timeout := range []any{0, -5, "35000"} {
		p := fixtureProfile("http://fixture.test")
		store := Store{t.TempDir()}
		_ = store.save(p, false)
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return jsonHTTP(envelope(p, "ok", map[string]any{"execution_timeout_ms": timeout}), 200), nil
		})}
		code, out, _, _ := run(t, store, client, "", "invoke", "fixture.read", "--env", p.EnvironmentID)
		if code != 1 || calls != 1 || stringField(objectField(out, "error"), "code") != "protocol_error" {
			t.Fatalf("malformed timeout %v was invoked: out=%+v calls=%d", timeout, out, calls)
		}
	}
}

// A server newer than the CLI may advertise larger limits than the CLI
// knows. The CLI uses them rather than refusing the operation: it waits as
// long as the server says the operation runs, and sends at most the smaller
// of its own request bound and the server's. Only a malformed value is
// refused.
func TestAnOlderCLIAcceptsTheLargerLimitsOfANewerServer(t *testing.T) {
	p := fixtureProfile("http://fixture.test")
	p.Scope = sessionScope
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonHTTP(envelope(p, "ok", map[string]any{"effect": "read", "execution_timeout_ms": 600000,
				"request_limits":    map[string]any{"admission_timeout_ms": 60000, "transport_margin_ms": 60000},
				"request_max_bytes": int64(maxInputBytes) * 4}), 200), nil
		}
		deadline, ok := r.Context().Deadline()
		if remaining := time.Until(deadline); !ok || remaining <= 719*time.Second || remaining > 720*time.Second {
			t.Fatalf("deadline %v %s, want the server's 600 s + 60 s + 60 s", ok, remaining)
		}
		return jsonHTTP(envelope(p, "ok", map[string]any{"value": 1}), 200), nil
	})}
	if code, out, _, _ := run(t, store, client, "", "invoke", "fixture.read", "--env", p.EnvironmentID, "--input", `{"a":1}`); code != 0 || calls != 2 {
		t.Fatalf("an operation with larger server limits was refused: exit %d calls %d %+v", code, calls, out)
	}
}

// The request bound is the smaller of the two: a server allowing more than
// the CLI's own bound still gets no request larger than the CLI's.
func TestRequestBoundIsTheSmallerOfCLIAndServer(t *testing.T) {
	if got := requestBound(int64(maxInputBytes) * 4); got != maxInputBytes {
		t.Fatalf("bound %d, want the CLI's %d", got, maxInputBytes)
	}
	if got := requestBound(1024); got != 1024 {
		t.Fatalf("bound %d, want the server's 1024", got)
	}
}
