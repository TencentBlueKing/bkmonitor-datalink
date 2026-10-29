// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// strategy.board reads the diagnosis's board with the page's selection and
// nothing else, and is a front read: the admin page lists strategies from it.
func TestStrategyBoardReadsTheDiagnosisBoardAsAFrontRead(t *testing.T) {
	var asked *url.URL
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL
		_, _ = w.Write([]byte(`{"generated_at":"2026-10-17T00:00:00Z","max_age_seconds":30,"universe_digest":"d","universe":1,` +
			`"facets":{"state":{},"action":{},"capability":{},"punctuality":{}},"late_objects":{"value":0},"late_strategies":{"value":0},` +
			`"matched":1,"rows":[{"strategy_id":"1","state":"DETECTING","state_word":"在检测","capability_class":"supported","line":"在检测","punctuality":"on_time","kb_anchor":"state-DETECTING"}]}`))
	})
	c := testChannel(t, &testAuth{}, NativeOperations(native)...)
	status, out := call(t, c, envelope(c, "invoke", "strategy.board", Params{"state": "DATA_ABSENT,DETECTING", "punctuality": "late", "limit": 20}))
	if status != 200 || asked == nil || asked.Path != "/api/diagnose" {
		t.Fatalf("status %d asked %v: %+v", status, asked, out)
	}
	query := asked.Query()
	if query.Get("board") != "1" || query.Get("state") != "DATA_ABSENT,DETECTING" || query.Get("punctuality") != "late" ||
		query.Get("limit") != "20" || query.Has("cursor") || query.Has("summary") || query.Has("action") {
		t.Fatalf("asked %q, want board=1 with the given selection only", asked.RawQuery)
	}
	if !FrontReads["strategy.board"] {
		t.Fatal("strategy.board is not a front read")
	}
}

// The overview's drill keys pass through as the board's area and reason, and
// stay on the next page's call.
func TestStrategyBoardPassesTheOverviewsDrillKeys(t *testing.T) {
	var asked *url.URL
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL
		_, _ = w.Write([]byte(`{"generated_at":"2026-10-17T00:00:00Z","max_age_seconds":30,"universe_digest":"d","universe":1,` +
			`"facets":{"state":{},"action":{},"capability":{},"punctuality":{}},"late_objects":{"value":0},"late_strategies":{"value":0},` +
			`"matched":2,"rows":[],"next_cursor":"next"}`))
	})
	c := testChannel(t, &testAuth{}, NativeOperations(native)...)
	_, out := call(t, c, envelope(c, "invoke", "strategy.board", Params{"area": "source", "capability": "not_yet_supported", "reason": "QUERY_SOURCE_NOT_MIGRATED"}))
	if asked == nil || asked.Query().Get("area") != "source" || asked.Query().Get("reason") != "QUERY_SOURCE_NOT_MIGRATED" {
		t.Fatalf("asked %v", asked)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"area":"source"`) || !strings.Contains(string(raw), `"reason":"QUERY_SOURCE_NOT_MIGRATED"`) {
		t.Fatalf("the next page dropped the drill keys: %s", raw)
	}
}

// A universe the diagnosis cannot read is the op's failure, said by name, not
// an empty board.
func TestStrategyBoardSaysAnUnreadableUniverse(t *testing.T) {
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"DIAGNOSIS_UNREADABLE","reason":"SOURCE_UNREADABLE"}`))
	})
	c := testChannel(t, &testAuth{}, NativeOperations(native)...)
	_, out := call(t, c, envelope(c, "invoke", "strategy.board", Params{}))
	raw, _ := json.Marshal(out)
	encoded := strings.ToLower(string(raw))
	if !strings.Contains(encoded, "diagnosis_unreadable") || !strings.Contains(encoded, "source_unreadable") {
		t.Fatalf("an unreadable universe answered %s", encoded)
	}
}

// The urgency facet passes through to the board, and stays on the next page's
// call with the rest of the selection.
func TestStrategyBoardPassesTheUrgencyFacet(t *testing.T) {
	var asked *url.URL
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL
		_, _ = w.Write([]byte(`{"generated_at":"2026-10-17T00:00:00Z","max_age_seconds":30,"universe_digest":"d","universe":3,` +
			`"facets":{"state":{},"action":{},"capability":{},"punctuality":{},"urgency":{}},"late_objects":{"value":0},"late_strategies":{"value":0},` +
			`"matched":2,"rows":[],"next_cursor":"next"}`))
	})
	c := testChannel(t, &testAuth{}, NativeOperations(native)...)
	_, out := call(t, c, envelope(c, "invoke", "strategy.board", Params{"urgency": "ACTION_NEEDED,UNKNOWN"}))
	if asked == nil || asked.Query().Get("urgency") != "ACTION_NEEDED,UNKNOWN" {
		t.Fatalf("asked %v", asked)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"urgency":"ACTION_NEEDED,UNKNOWN"`) {
		t.Fatalf("the next page dropped the urgency facet: %s", raw)
	}
}
