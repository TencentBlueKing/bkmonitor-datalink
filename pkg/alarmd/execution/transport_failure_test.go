// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

type transportTimeout struct{ timeout bool }

func (e transportTimeout) Error() string   { return "i/o timeout" }
func (e transportTimeout) Timeout() bool   { return e.timeout }
func (e transportTimeout) Temporary() bool { return e.timeout }

// One function names an http.Client.Do failure for every dependency that
// reads one, from the error's types and never its text, which may carry the
// endpoint. Each word is tested on both sides: the shape it names, and a
// shape close to it that it must not. The expectations are what the Go
// standard library's error types mean, not what alarmd happens to return.
func TestClassifyTransportFailureNamesEachShapeAndNotItsNeighbour(t *testing.T) {
	wrap := func(err error) error {
		return &url.Error{Op: "Get", URL: "https://user:secret@192.0.2.10/local-api/x", Err: err}
	}
	syscallErr := func(op string, errno syscall.Errno) error {
		return &net.OpError{Op: op, Net: "tcp", Err: &os.SyscallError{Syscall: op, Err: errno}}
	}
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, TransportFailureOther},
		// The caller let go: named before anything the cancellation caused.
		{"cancelled", wrap(context.Canceled), TransportFailureCancelled},
		{"cancelled during a read that timed out", wrap(fmt.Errorf("%w: %w", context.Canceled, transportTimeout{true})), TransportFailureCancelled},
		{"deadline", wrap(context.DeadlineExceeded), TransportFailureTimeout},
		{"net timeout", wrap(&net.OpError{Op: "read", Err: transportTimeout{true}}), TransportFailureTimeout},
		{"a net error that is not a timeout", wrap(&net.OpError{Op: "read", Err: transportTimeout{false}}), TransportFailureOther},
		{"dns", wrap(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "console.example.test"}}), TransportFailureDNS},
		{"dns that timed out on its own", wrap(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "i/o timeout", Name: "console.example.test", IsTimeout: true}}), TransportFailureDNS},
		// The caller's budget ran out while resolving: the budget is the fact.
		{"dns that ran out the caller's deadline", wrap(fmt.Errorf("%w: %w", &net.DNSError{Err: "lookup", Name: "console.example.test"}, context.DeadlineExceeded)), TransportFailureTimeout},
		{"tls unknown authority", wrap(x509.UnknownAuthorityError{}), TransportFailureTLS},
		{"tls verification", wrap(&tls.CertificateVerificationError{Err: x509.HostnameError{}}), TransportFailureTLS},
		{"tls record header", wrap(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), TransportFailureTLS},
		{"refused", wrap(syscallErr("dial", syscall.ECONNREFUSED)), TransportFailureConnectionRefused},
		{"reset", wrap(syscallErr("read", syscall.ECONNRESET)), TransportFailureConnectionReset},
		{"broken pipe", wrap(syscallErr("write", syscall.EPIPE)), TransportFailureConnectionReset},
		{"reset is not refused", wrap(syscallErr("read", syscall.ECONNRESET)), TransportFailureConnectionReset},
		{"eof", wrap(io.EOF), TransportFailureEOF},
		{"unexpected eof", wrap(io.ErrUnexpectedEOF), TransportFailureEOF},
		{"other", wrap(errors.New("something else")), TransportFailureOther},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyTransportFailure(test.err); got != test.want {
				t.Fatalf("ClassifyTransportFailure(%v) = %q, want %q", test.err, got, test.want)
			}
		})
	}
}

// The route detail a provider attempt carries keeps its own closed words:
// a cancelled caller is not a provider's failure and reads as other there,
// as it did before the word existed.
func TestTheRouteDetailKeepsCancelledOutOfItsWords(t *testing.T) {
	if got := TransportRouteDetail(TransportFailureCancelled); got != "transport=other" {
		t.Fatalf("route detail of a cancelled caller = %q, want transport=other", got)
	}
	for _, word := range TransportFailureWords {
		if word == TransportFailureCancelled {
			continue
		}
		if got := TransportRouteDetail(word); got != "transport="+word {
			t.Fatalf("route detail of %s = %q", word, got)
		}
	}
	if strings.Join(TransportFailureWords, ",") != "cancelled,timeout,connection_refused,connection_reset,dns,tls,eof,other" {
		t.Fatalf("words %v", TransportFailureWords)
	}
}
