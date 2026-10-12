// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// An excerpt keeps the provider's reason and replaces what names where it
// runs or what lets one in: URLs, credential values, addresses.
func TestAnErrorExcerptKeepsTheReasonAndDropsCredentialsAndAddresses(t *testing.T) {
	body := `{"error":"index set 12 not found in space bkcc__2",` + "\n\t" +
		`"trace":"http://uq.example.test:10205/query/ts?token=abc","token":"s3cr3t","Authorization: Bearer xyz",` +
		`"backend":"192.0.2.7:9200","password=hunter2"}`
	excerpt := providerErrorExcerpt([]byte(body))
	for _, gone := range []string{"s3cr3t", "xyz", "hunter2", "192.0.2.7", "uq.example.test", "abc", "\n", "\t"} {
		if strings.Contains(excerpt, gone) {
			t.Errorf("excerpt %q still has %q", excerpt, gone)
		}
	}
	if !strings.Contains(excerpt, "index set 12 not found in space bkcc__2") {
		t.Fatalf("excerpt %q, want the provider's reason kept", excerpt)
	}
}

// Credentials a provider echoes inside an escaped JSON string, keys that
// only contain a credential word, bracketed IPv6 and host:port addresses,
// and a value the read cut off are replaced too.
func TestAnErrorExcerptReplacesEscapedAndPartialCredentials(t *testing.T) {
	for name, body := range map[string]string{
		"escaped json":    `{"error":"bad request {\"password\":\"p4ss\",\"bk_app_secret\":\"s3c\"}"}`,
		"escaped header":  `{"error":"refused","X-Bkapi-Authorization":"{\"bk_app_code\":\"a\",\"bk_app_secret\":\"s3c\"}"}`,
		"keys by word":    `refused api_key=k3y&secret_key=sk&access-token=t0k`,
		"cut off":         `{"error":"bad","password":"p4ss`,
		"ipv6 and a host": `backend [2001:db8::1]:8481 and vm-select.example.test:8481 did not answer`,
	} {
		excerpt := providerErrorExcerpt([]byte(body))
		for _, gone := range []string{"p4ss", "s3c", "k3y", "sk&", "t0k", "2001:db8", "vm-select.example.test"} {
			if strings.Contains(excerpt, gone) {
				t.Errorf("%s: excerpt %q still has %q", name, excerpt, gone)
			}
		}
	}
	if got := providerErrorExcerpt([]byte(`backend [2001:db8::1]:8481 did not answer`)); got != "backend <address> did not answer" {
		t.Fatalf("excerpt %q, want the address replaced and the reason kept", got)
	}
}

// An excerpt is cut to its bound on a rune boundary, and a body that is not
// valid text is still one.
func TestAnErrorExcerptIsBounded(t *testing.T) {
	long := providerErrorExcerpt([]byte("a" + strings.Repeat("错", 400)))
	if len(long) > errorExcerptBytes+len("...") || !strings.HasSuffix(long, "...") || !strings.HasPrefix(long, "a错") ||
		!utf8.ValidString(long) {
		t.Fatalf("excerpt of %d bytes, want a body of at most %d bytes and the cut marked", len(long), errorExcerptBytes)
	}
	if got := providerErrorExcerpt([]byte{'a', 0xff, 'b'}); got != "a?b" {
		t.Fatalf("excerpt %q, want the invalid byte replaced", got)
	}
	if got := providerErrorExcerpt(nil); got != "" {
		t.Fatalf("excerpt %q of no body, want none", got)
	}
}

// Detection never keeps the body: a query refused by the provider is its
// status alone. Only the diagnostic read keeps an excerpt.
func TestOnlyADiagnosticReadKeepsTheErrorBody(t *testing.T) {
	body := `{"error":"index set 12 not found"}`
	client := fixtureClient(t, http.StatusBadRequest, body, DefaultLimits())
	completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
	if attempt := failedAttempt(t, completion, err); attempt.Detail != "http_status=400" {
		t.Fatalf("attempt %+v, want the status alone", attempt)
	}
	diagnostic := diagnosticFixture(t, http.StatusBadRequest, body)
	result, err := diagnostic.Query(context.Background(), validAttempt(t).Spec, DiagnosticSelection{})
	if err != nil || result.Completion == nil || result.Completion.ErrorExcerpt != body ||
		len(result.Completion.RouteDetails) != 1 || result.Completion.RouteDetails[0] != "http_status=400" {
		t.Fatalf("diagnostic %+v error %v, want the status beside the body's excerpt", result.Completion, err)
	}
}
