// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

func TestThePageIsServedAtWhateverRootItIsMountedUnder(t *testing.T) {
	for _, target := range []string{"/", "/index.html"} {
		response := get(t, target)
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d, want 200", target, response.Code)
		}
		if !strings.Contains(response.Body.String(), "alarmd 对象与判定") {
			t.Fatalf("%s did not serve the page", target)
		}
	}
}

// A host proxies a whole prefix here, so every path it does not recognise
// arrives at this handler. Answering all of them with the page would turn a
// mistyped API path into an HTML body that the caller parses as JSON and
// reports as a decode failure instead of as a missing route.
func TestUnknownPathsAreNotFoundRatherThanThePage(t *testing.T) {
	for _, target := range []string{"/api/health", "/objects", "/anything"} {
		if response := get(t, target); response.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", target, response.Code)
		}
	}
}

// The page is meant to be framed by a host. A header forbidding that would make
// the delivery model impossible while every test still passed.
func TestThePageDoesNotForbidBeingFramed(t *testing.T) {
	response := get(t, "/")
	for _, header := range []string{"X-Frame-Options", "Content-Security-Policy"} {
		if value := response.Header().Get(header); value != "" {
			t.Fatalf("%s = %q, want the page to stay embeddable", header, value)
		}
	}
}

// Every request the page makes is resolved against its own location, because it
// cannot know the prefix a host mounted it under. An absolute path would work in
// exactly one deployment -- alarmd's own port -- and break every host.
func TestThePageAddressesTheAPIRelativeToItself(t *testing.T) {
	body := get(t, "/").Body.String()
	if !strings.Contains(body, "BASE + 'api/'") {
		t.Fatal("the page no longer resolves the API against its own location")
	}
	for _, absolute := range []string{"'/api/", "\"/api/", "fetch('/"} {
		if strings.Contains(body, absolute) {
			t.Fatalf("the page contains an absolute API path %q, which only works on alarmd's own port", absolute)
		}
	}
}
