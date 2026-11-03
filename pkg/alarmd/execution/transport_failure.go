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
	"io"
	"net"
	"syscall"
)

// TransportFailureCancelled is a call whose caller let go of it: the
// process stopping, a lease let go. Not a dependency's failure, so a
// provider attempt's route detail reads it as other (TransportRouteDetail);
// a dependency whose record keeps its own failure class names it.
const TransportFailureCancelled = "cancelled"

// TransportFailureWords is every word ClassifyTransportFailure returns, in
// the order it decides them.
var TransportFailureWords = []string{TransportFailureCancelled, TransportFailureTimeout,
	TransportFailureConnectionRefused, TransportFailureConnectionReset, TransportFailureDNS, TransportFailureTLS,
	TransportFailureEOF, TransportFailureOther}

// ClassifyTransportFailure maps an http.Client.Do error, or a failed read of
// a response body, onto the bounded transport words; every dependency that
// names such a failure names it here, so one error reads the same word
// whichever dependency it came from. It inspects error types only and never
// copies the error text, which may embed the endpoint URL.
//
// A caller's cancellation is named first. The caller's deadline comes before
// what the call was doing when it ran out: a lookup that used up the budget
// reads timeout, and only a resolver failing inside the budget reads dns.
func ClassifyTransportFailure(err error) string {
	if err == nil {
		return TransportFailureOther
	}
	if errors.Is(err, context.Canceled) {
		return TransportFailureCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return TransportFailureTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return TransportFailureDNS
	}
	if isTLSFailure(err) {
		return TransportFailureTLS
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return TransportFailureConnectionRefused
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return TransportFailureConnectionReset
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return TransportFailureEOF
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return TransportFailureTimeout
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return TransportFailureConnectionRefused
	}
	return TransportFailureOther
}

func isTLSFailure(err error) bool {
	var recordHeader tls.RecordHeaderError
	var alert tls.AlertError
	var certificate *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificateInvalid x509.CertificateInvalidError
	return errors.As(err, &recordHeader) || errors.As(err, &alert) || errors.As(err, &certificate) ||
		errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &certificateInvalid)
}
