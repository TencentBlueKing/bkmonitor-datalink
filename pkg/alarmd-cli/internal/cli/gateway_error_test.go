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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failure written by a proxy or gateway in front of alarmd is named as
// one, by its HTTP status and without its body - on the describe step and on
// the call alike - while a failure alarmd answered keeps its own code, and
// one it answered on another channel version is that skew, not a gateway. The
// gateway's page was printed as the result: its fields, no error code, and
// nothing saying it was not alarmd's answer.
func TestAGatewaysFailureIsNamedAndItsPageIsNotTheResult(t *testing.T) {
	gatewayPage := `{"Contents":["unknown error","please contact the hotline <a href=\"chat://help\">here</a>"],"code":1,"desc":"gateway","version":"1"}`
	for _, step := range []string{"describe", "invoke"} {
		t.Run(step, func(t *testing.T) {
			var p Profile
			calls := 0
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if step == "invoke" && calls == 1 {
					writeJSON(t, w, envelope(p, "ok", map[string]any{}))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(gatewayPage))
			}))
			defer s.Close()
			p = fixtureProfile(s.URL)
			store := Store{t.TempDir()}
			_ = store.save(p, false)
			code, result, stdout, _ := run(t, store, s.Client(), "", "invoke", "store.inspect", "--env", p.EnvironmentID)
			failure := objectField(result, "error")
			if code != 1 || stringField(failure, "code") != "gateway_error" || failure["http_status"] != float64(http.StatusBadGateway) ||
				objectField(result, "meta")["http_status"] != float64(http.StatusBadGateway) {
				t.Fatalf("a gateway's 502 = exit %d %v, want gateway_error with its status", code, result)
			}
			if strings.Contains(stdout, "Contents") || strings.Contains(stdout, "hotline") {
				t.Fatalf("the gateway's page reached the output: %s", stdout)
			}
		})
	}

	// alarmd's failure on another channel version is alarmd's, answered across
	// a version this CLI does not speak - not a gateway's - with its code kept.
	var skewed Profile
	skew := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := envelope(skewed, "error", nil)
		objectField(m, "meta")["channel_version"] = "alarmd-ob/v2"
		m["error"] = map[string]any{"code": "target_unreachable", "message": "the owner did not answer"}
		w.WriteHeader(http.StatusBadGateway)
		writeJSON(t, w, m)
	}))
	defer skew.Close()
	skewed = fixtureProfile(skew.URL)
	skewStore := Store{t.TempDir()}
	_ = skewStore.save(skewed, false)
	code, result, _, _ := run(t, skewStore, skew.Client(), "", "discover", "--env", skewed.EnvironmentID)
	failure := objectField(result, "error")
	if code != 1 || stringField(failure, "code") != "protocol_error" || stringField(failure, "server_error_code") != "target_unreachable" ||
		stringField(objectField(result, "meta"), "server_channel_version") != "alarmd-ob/v2" {
		t.Fatalf("alarmd's 502 on another channel version = exit %d %v, want a channel version failure keeping its code", code, result)
	}

	// alarmd's own failure, with its channel meta, is reported as it was.
	var p Profile
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := envelope(p, "error", nil)
		m["error"] = map[string]any{"code": "target_unreachable", "message": "the owner did not answer"}
		w.WriteHeader(http.StatusBadGateway)
		writeJSON(t, w, m)
	}))
	defer s.Close()
	p = fixtureProfile(s.URL)
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	if code, result, _, _ := run(t, store, s.Client(), "", "discover", "--env", p.EnvironmentID); code != 1 ||
		stringField(objectField(result, "error"), "code") != "target_unreachable" {
		t.Fatalf("alarmd's own 502 = exit %d %v, want its own code", code, result)
	}
}
