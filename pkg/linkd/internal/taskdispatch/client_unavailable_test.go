// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package taskdispatch

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"linkd/internal/processingerror"
)

func TestControlPlaneHTTPResponsesDoNotEnterOutageWait(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"success", 200, `{}`},
		{"not found", 404, "private configuration"},
		{"invalid query", 400, "private configuration"},
		{"invalid target", 422, "private configuration"},
		{"server error", 500, "private configuration"},
		{"bad gateway", 502, "private configuration"},
		{"unavailable", 503, "private configuration"},
		{"gateway timeout", 504, "private configuration"},
		{"request timeout", 408, "private configuration"},
		{"rate limited", 429, "private configuration"},
		{"worker unauthorized", 401, "private configuration"},
		{"assignment revoked", 403, "private configuration"},
		{"complete but invalid JSON", 200, `{`},
		{"wrong JSON type", 200, `[]`},
		{"empty response", 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			var result map[string]any
			err := (Client{URL: server.URL}).Call(t.Context(), http.MethodGet, "/internal/policy-catalog", nil, &result)
			if errors.Is(err, processingerror.ErrControlPlaneUnavailable) {
				t.Fatal("HTTP response mistaken for transport failure", err)
			}
			if (err == nil) != (tc.name == "success") {
				t.Fatal("lost query outcome", err)
			}
			if tc.status != 200 {
				var response *ResponseError
				if !errors.As(err, &response) || response.StatusCode != tc.status {
					t.Fatal("lost HTTP status", err)
				}
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("response leaked", err)
			}
		})
	}
}

func TestControlPlaneConnectionFailuresEnterOutageWait(t *testing.T) {
	for _, kind := range []string{"connection refused", "closed before headers", "truncated body", "header timeout", "body timeout"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "closed before headers":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
				case "truncated body":
					w.Header().Set("Content-Length", "100")
					_, _ = w.Write([]byte(`{`))
				case "header timeout":
					<-r.Context().Done()
				case "body timeout":
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			if kind == "connection refused" {
				server.Close()
			}
			var result map[string]any
			err := (Client{URL: server.URL, HTTP: &http.Client{Timeout: 100 * time.Millisecond}}).Call(t.Context(), http.MethodGet, "/internal/policy-catalog", nil, &result)
			if !errors.Is(err, processingerror.ErrControlPlaneUnavailable) {
				t.Fatal("transport failure was not retained", err)
			}
		})
	}
}

type clientRoundTripFunc func(*http.Request) (*http.Response, error)

func (f clientRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestControlPlaneTransportClassificationIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		pending bool
	}{
		{"connection reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, true},
		{"broken pipe", syscall.EPIPE, true},
		{"connection aborted", syscall.ECONNABORTED, true},
		{"network unreachable", syscall.ENETUNREACH, true},
		{"host unreachable", syscall.EHOSTUNREACH, true},
		{"closed connection", net.ErrClosed, true},
		{"EOF before response", io.EOF, true},
		{"partial HTTP response", io.ErrUnexpectedEOF, true},
		{"request deadline", context.DeadlineExceeded, true},
		{"DNS temporary failure", &net.DNSError{Err: "private", IsTemporary: true}, true},
		{"DNS not found", &net.DNSError{Err: "private", IsNotFound: true}, false},
		{"certificate failure", x509.UnknownAuthorityError{}, false},
		{"unknown local failure", errors.New("private configuration failure"), false},
		{"caller cancellation", context.Canceled, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := Client{URL: "http://control-plane", HTTP: &http.Client{Transport: clientRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("private endpoint: %w", tc.err) })}}
			err := client.Call(t.Context(), http.MethodGet, "/internal/policy-catalog", nil, nil)
			if errors.Is(err, processingerror.ErrControlPlaneUnavailable) != tc.pending {
				t.Fatal("wrong transport classification", err)
			}
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal("transport error lost or leaked", err)
			}
			if errors.Is(tc.err, context.Canceled) && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
		})
	}
}
