// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
)

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestConnectionErrorsKeepActionableCausesWithoutSecrets(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"dns", &net.DNSError{Err: "fixture-secret", Name: "fixture-secret"}, "DNS resolution failed"},
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "connection refused"},
		{"timeout", context.DeadlineExceeded, "request timed out"},
		{"unknown", errors.New("proxy https://user:fixture-secret@proxy.invalid failed"), "request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{HTTP: &http.Client{Transport: failingTransport{tc.err}}}
			_, _, err := a.request(fixtureProfile("http://example.test"), http.MethodGet, "api/cli/session", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("incorrect or unsafe error: %v", err)
			}
		})
	}
}
