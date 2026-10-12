// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// failingTransport answers every request with err, or passes it to next
// when err is nil.
type failingTransport struct {
	err  error
	next http.RoundTripper
}

func (transport *failingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.err != nil {
		return nil, transport.err
	}
	return transport.next.RoundTrip(request)
}

type consoleFailure struct{ op, class string }

func classReader(t *testing.T, baseURL string, client *http.Client, failed func(op, class string, failures uint64)) *HTTPReconciler {
	t.Helper()
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: baseURL, Client: client, Username: "user", Password: "secret",
		Index: testIndex(), MaxResponseBytes: 1 << 20, OnFailure: failed})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

// A Console call that fails names how, in the record every reader takes it
// from: the transport's own word for a connection refused, a call that ran
// past its time, a certificate the client does not trust or a caller that
// let go; status for an answer other than 200; incomplete for a body that
// could not be read whole. Each failure counts under its word. Neither the
// failure text nor anything else in the record carries the address or the
// credentials.
func TestAConsoleFailureNamesItsCause(t *testing.T) {
	block := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	for _, c := range []struct {
		name, want string
		call       func(t *testing.T) (*HTTPReconciler, context.Context, string)
	}{
		{"refused", execution.TransportFailureConnectionRefused, func(t *testing.T) (*HTTPReconciler, context.Context, string) {
			server := httptest.NewServer(http.NotFoundHandler())
			address := server.URL
			server.Close()
			return classReader(t, address, &http.Client{}, nil), context.Background(), address
		}},
		{"timeout", execution.TransportFailureTimeout, func(t *testing.T) (*HTTPReconciler, context.Context, string) {
			server := httptest.NewServer(block)
			t.Cleanup(server.Close)
			return classReader(t, server.URL, &http.Client{Timeout: 100 * time.Millisecond}, nil), context.Background(), server.URL
		}},
		{"tls", execution.TransportFailureTLS, func(t *testing.T) (*HTTPReconciler, context.Context, string) {
			server := httptest.NewTLSServer(http.NotFoundHandler())
			t.Cleanup(server.Close)
			return classReader(t, server.URL, &http.Client{}, nil), context.Background(), server.URL
		}},
		{"cancelled", execution.TransportFailureCancelled, func(t *testing.T) (*HTTPReconciler, context.Context, string) {
			server := httptest.NewServer(block)
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
			time.AfterFunc(100*time.Millisecond, cancel)
			t.Cleanup(cancel)
			return classReader(t, server.URL, server.Client(), nil), ctx, server.URL
		}},
		{"status", ConsoleFailureStatus, func(t *testing.T) (*HTTPReconciler, context.Context, string) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
			t.Cleanup(server.Close)
			return classReader(t, server.URL, server.Client(), nil), context.Background(), server.URL
		}},
		{"incomplete", ConsoleFailureIncomplete, func(t *testing.T) (*HTTPReconciler, context.Context, string) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{not json")) }))
			t.Cleanup(server.Close)
			return classReader(t, server.URL, server.Client(), nil), context.Background(), server.URL
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			reader, ctx, address := c.call(t)
			if _, err := reader.Reconcile(ctx, keyA); err == nil {
				t.Fatal("the call succeeded")
			}
			call := reader.Record().Calls[ConsoleOpReconcile]
			if call.LastFailureClass != c.want || call.FailuresByClass[c.want] != 1 || call.Failures != 1 {
				t.Fatalf("record %+v, want one failure named %s", call, c.want)
			}
			host := strings.TrimPrefix(strings.TrimPrefix(address, "https://"), "http://")
			if strings.Contains(call.LastFailure, host) || strings.Contains(call.LastFailure, "secret") {
				t.Fatalf("the failure text %q carries the address or the credentials", call.LastFailure)
			}
		})
	}
}

// A failure no word fits is named other, never left blank.
func TestAConsoleFailureNoWordFitsIsOther(t *testing.T) {
	reader := classReader(t, "http://192.0.2.10", &http.Client{Transport: &failingTransport{err: errors.New("boom")}}, nil)
	if _, err := reader.Reconcile(context.Background(), keyA); err == nil {
		t.Fatal("the call succeeded")
	}
	if call := reader.Record().Calls[ConsoleOpReconcile]; call.LastFailureClass != execution.TransportFailureOther {
		t.Fatalf("class %q, want other", call.LastFailureClass)
	}
}

// One error reads the same word on the Console as in a query provider's
// route detail: both take it from the one classifier. The one word that
// differs does so by design - a cancelled caller is not a provider's
// failure, and the route detail reads it as other - and is asserted as the
// difference it is.
func TestTheConsoleAndTheRouteDetailNameOneErrorAlike(t *testing.T) {
	op := func(name string, errno syscall.Errno) error {
		return &net.OpError{Op: name, Net: "tcp", Err: &os.SyscallError{Syscall: name, Err: errno}}
	}
	dns := &net.DNSError{Err: "no such host", Name: "console.example.test"}
	for _, c := range []struct {
		err  error
		want string
	}{
		{context.Canceled, execution.TransportFailureCancelled},
		{context.DeadlineExceeded, execution.TransportFailureTimeout},
		{op("dial", syscall.ECONNREFUSED), execution.TransportFailureConnectionRefused},
		{op("read", syscall.ECONNRESET), execution.TransportFailureConnectionReset},
		{&net.OpError{Op: "dial", Err: dns}, execution.TransportFailureDNS},
		// A lookup that used up the caller's budget: the budget is the fact.
		{fmt.Errorf("%w: %w", dns, context.DeadlineExceeded), execution.TransportFailureTimeout},
		{io.ErrUnexpectedEOF, execution.TransportFailureEOF},
		{errors.New("boom"), execution.TransportFailureOther},
	} {
		err := c.err
		transport := &failingTransport{err: err}
		reader := classReader(t, "http://192.0.2.10", &http.Client{Transport: transport}, nil)
		_, _ = reader.Reconcile(context.Background(), keyA)
		console := reader.Record().Calls[ConsoleOpReconcile].LastFailureClass
		if console != c.want {
			t.Fatalf("%v: the Console reads %q, want %q", err, console, c.want)
		}
		wrapped := &url.Error{Op: "Get", URL: "http://192.0.2.10", Err: err}
		route := execution.TransportRouteDetail(execution.ClassifyTransportFailure(wrapped))
		want := "transport=" + console
		if console == execution.TransportFailureCancelled {
			want = "transport=" + execution.TransportFailureOther
		}
		if route != want {
			t.Fatalf("%v: the Console reads %q and the route detail %q", err, console, route)
		}
	}
}

// A failure is told once when it starts and again when its cause changes,
// not on every call while it lasts: the roster pages and every strategy's
// reconciliation would otherwise write a line each through an outage. A
// success ends the run, and the next failure is told again.
func TestAConsoleFailureIsToldWhenItStartsAndWhenItsCauseChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/local-api/strategy-index/targets":
			_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
		default:
			_ = json.NewEncoder(w).Encode(reconciliationJSON())
		}
	}))
	t.Cleanup(server.Close)
	var told []consoleFailure
	transport := &failingTransport{next: http.DefaultTransport}
	reader := classReader(t, server.URL, &http.Client{Transport: transport}, func(op, class string, failures uint64) {
		// The reconciliation's own record; the event source a successful
		// one also reads is its own operation, not answered here.
		if op == ConsoleOpReconcile {
			told = append(told, consoleFailure{op, class})
		}
	})
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
	reset := &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}
	for _, err := range []error{refused, refused, refused, reset, reset, nil, refused} {
		transport.err = err
		_, _ = reader.Reconcile(context.Background(), keyA)
	}
	want := []consoleFailure{{ConsoleOpReconcile, execution.TransportFailureConnectionRefused},
		{ConsoleOpReconcile, execution.TransportFailureConnectionReset}, {ConsoleOpReconcile, execution.TransportFailureConnectionRefused}}
	if len(told) != len(want) {
		t.Fatalf("told %+v, want %+v", told, want)
	}
	for i := range want {
		if told[i] != want[i] {
			t.Fatalf("told %+v, want %+v", told, want)
		}
	}
	if call := reader.Record().Calls[ConsoleOpReconcile]; call.FailuresByClass[execution.TransportFailureConnectionRefused] != 4 ||
		call.FailuresByClass[execution.TransportFailureConnectionReset] != 2 || call.Failures != 6 {
		t.Fatalf("counts by class %+v", call.FailuresByClass)
	}
}

// The record handed out is the record at that moment: a failure after it
// does not change the counts by class it already carries.
func TestARecordHandedOutKeepsItsCountsByClass(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
	reader := classReader(t, "http://192.0.2.10", &http.Client{Transport: &failingTransport{err: refused}}, nil)
	_, _ = reader.Reconcile(context.Background(), keyA)
	before := reader.Record().Calls[ConsoleOpReconcile]
	_, _ = reader.Reconcile(context.Background(), keyA)
	if before.FailuresByClass[execution.TransportFailureConnectionRefused] != 1 {
		t.Fatalf("a record handed out before the second failure now reads %v", before.FailuresByClass)
	}
	if after := reader.Record().Calls[ConsoleOpReconcile]; after.FailuresByClass[execution.TransportFailureConnectionRefused] != 2 {
		t.Fatalf("the record now reads %v", after.FailuresByClass)
	}
}
