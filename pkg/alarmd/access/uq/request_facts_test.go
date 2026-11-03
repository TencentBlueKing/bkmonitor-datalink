// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A request the transport failed with a deadline error of its own, its
// attempt's deadline forty seconds away, says so on its failed attempt:
// which deadline was on the request and how much of it was left at the send,
// that the attempt's own deadline set it, that neither context had run out,
// the error sanitized of its address and credentials, and the Go types along
// its chain. A deadline error alone named none of these.
func TestATransportDeadlineSaysWhichDeadlineWasOnTheRequest(t *testing.T) {
	transportErr := &url.Error{Op: "Post", URL: "http://reader:Hunter9Secret@192.0.2.10:8080/query/ts",
		Err: &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", context.DeadlineExceeded)}}
	httpClient := &http.Client{Transport: diagnosticRoundTripper(func(*http.Request) (*http.Response, error) { return nil, transportErr })}
	client, err := NewClient("http://uq.example.test", "alarmd", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	attempt := validAttempt(t)
	attempt.DeadlineUnixMilli = time.Now().Add(40 * time.Second).UnixMilli()
	completion, err := client.Execute(context.Background(), attempt, &collectingSink{})
	if err != nil {
		t.Fatalf("a transport failure returned %v, want an unavailable completion", err)
	}
	failed := completion.RouteFacts.Attempts[0]
	if failed.ReasonCode != execution.ReasonCode(contract.ReasonQueryTimeout) || !strings.HasPrefix(failed.Detail, "transport=") {
		t.Fatalf("attempt %+v, want the transport's timeout", failed)
	}
	request := failed.Request
	if request == nil {
		t.Fatal("the failed attempt carries no request facts")
	}
	if request.DeadlineUnixMilli != attempt.DeadlineUnixMilli || request.DeadlineLeftAtDoMs < 38_000 || request.DeadlineLeftAtDoMs > 40_000 ||
		request.DeadlineSource != execution.RequestDeadlineAttempt || request.Expired != execution.RequestExpiredNone {
		t.Fatalf("request facts %+v, want the attempt's deadline about 40 s away, set by the attempt, and nothing expired", request)
	}
	if len(request.ErrorTypes) == 0 || request.ErrorTypes[0] != "*url.Error" ||
		!strings.Contains(strings.Join(request.ErrorTypes, " > "), "*net.OpError") {
		t.Fatalf("error types %v, want the chain from *url.Error down", request.ErrorTypes)
	}
	if request.ErrorChain == "" || strings.Contains(request.ErrorChain, "Hunter9Secret") || strings.Contains(request.ErrorChain, "192.0.2.10") ||
		len(request.ErrorChain) > execution.MaxRequestErrorBytes {
		t.Fatalf("error chain %q, want it sanitized and bounded", request.ErrorChain)
	}

	// A caller whose own deadline is the earlier one set the request's.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // fixed-wait: a caller deadline earlier than the attempt's, which the case needs
	defer cancel()
	completion, err = client.Execute(ctx, attempt, &collectingSink{})
	if err != nil {
		t.Fatal(err)
	}
	if request := completion.RouteFacts.Attempts[0].Request; request == nil || request.DeadlineSource != execution.RequestDeadlineCaller {
		t.Fatalf("request facts %+v, want the caller's deadline named", request)
	}
}

// A provider that answered with a failing status names, on the failed
// attempt, the trace id the request was sent with: the round's own line
// takes the failed query's request facts, and a 503 has no rejection of its
// own to say it there.
func TestAFailingStatusKeepsTheTraceItWasSentWith(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusBadRequest} {
		var sent string
		httpClient := &http.Client{Transport: diagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
			sent = request.Header.Get(headerTraceParent)
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"error":"no"}`)), Header: http.Header{}}, nil
		})}
		client, err := NewClient("http://uq.example.test", "alarmd", httpClient)
		if err != nil {
			t.Fatal(err)
		}
		completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(sent, "-")
		if request := completion.RouteFacts.Attempts[0].Request; request == nil || len(parts) != 4 || request.TraceID != parts[1] {
			t.Fatalf("%d: request facts %+v, want the sent trace id of %q", status, request, sent)
		}
	}
}
