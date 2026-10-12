// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"net/http"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// absent.list reaches the page with the parameters given and nothing else,
// takes its filters from the page's own closed words, and says in its own
// description what the page cannot say.
func TestAbsentListReadsThePageAndSaysWhatItCannot(t *testing.T) {
	var asked string
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RequestURI()
		_, _ = w.Write([]byte(`{"answered_by":"r","state":"ready","rows":[]}`))
	})
	c := testChannel(t, &testAuth{}, NativeOperations(native)...)
	status, out := call(t, c, envelope(c, "invoke", "absent.list", Params{"outcome": "close_decided", "limit": 7}))
	if status != 200 || !strings.HasPrefix(asked, "/api/absent?") || !strings.Contains(asked, "outcome=close_decided") ||
		!strings.Contains(asked, "limit=7") || strings.Contains(asked, "execution=") || strings.Contains(asked, "cursor=") {
		t.Fatalf("status %d, asked %q: %+v", status, asked, out)
	}
	var op Operation
	for _, candidate := range NativeOperations(native) {
		if candidate.ID == "absent.list" {
			op = candidate
		}
	}
	if strings.Join(op.Fields["outcome"].Enum, ",") != strings.Join(fleet.AbsentOutcomes, ",") ||
		strings.Join(op.Fields["execution"].Enum, ",") != strings.Join(fleet.AbsentExecutions, ",") {
		t.Fatalf("the filters are not the page's words: %+v", op.Fields)
	}
	for _, limit := range []string{"拆分前的旧策略表", "⌈候选数/8⌉ 轮 × 5 分钟", "source_now=document 只说明文档键还在", "table.at 早于 last_round.at"} {
		if !strings.Contains(op.Summary, limit) {
			t.Fatalf("the description does not say %q: %s", limit, op.Summary)
		}
	}
}

// fleet.get points to the page where the deployment has the Console, and
// nowhere else.
func TestFleetGetPointsToTheCandidatePageWhereTheConsoleIs(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		next bool
	}{
		"configured":     {`{"linkd_console":{"needed":true,"state":"healthy"}}`, true},
		"not configured": {`{"linkd_console":{"needed":false,"state":"not_configured"}}`, false},
		"no standing":    {`{}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) })
			c := testChannel(t, &testAuth{}, NativeOperations(native)...)
			_, out := call(t, c, envelope(c, "invoke", "fleet.get", Params{}))
			found := false
			for _, next := range out.Next {
				found = found || next.Operation == "absent.list"
			}
			if found != tc.next {
				t.Fatalf("next calls %+v, want absent.list %v", out.Next, tc.next)
			}
		})
	}
}
