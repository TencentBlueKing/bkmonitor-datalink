// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A filtered close's notice is written with what places it -- the strategy,
// the decision, the expectation and what the dry run matched -- once per
// outcome and strategy a period; the lines held back are counted on the next.
// Another strategy, or another outcome, has its own line at once.
func TestAFilteredCloseNoticeIsWrittenOncePerStrategyAPeriod(t *testing.T) {
	var out bytes.Buffer
	at := time.Unix(1_800_000_000, 0)
	report := newFilterCloseReporter(observability.New("control", &out), func() time.Time { return at })
	refused := func(strategy string) filterclose.Notice {
		return filterclose.Notice{Outcome: filterclose.OutcomeExpectExceeded, Scope: filterclose.Scope{TenantID: "system", StrategyID: strategy},
			Decision: "absent", Expected: 1, Source: filterclose.ExpectOwnRecord, Allowed: 21, Matched: 48}
	}
	report(refused("802"))
	report(refused("802"))
	report(refused("802"))
	report(refused("804"))
	report(filterclose.Notice{Outcome: filterclose.OutcomeAlertsTruncated, Scope: filterclose.Scope{TenantID: "system", StrategyID: "802"}, Read: 500, Elapsed: 1500 * time.Millisecond})
	at = at.Add(filterCloseReportEvery)
	report(refused("802"))
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("line %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	if len(lines) != 4 {
		t.Fatalf("%d lines, want 802's first, 804's, 802's truncation and 802's next after the period: %v", len(lines), lines)
	}
	first, last := lines[0], lines[3]
	if first["strategy_id"] != "802" || first["decision"] != "absent" || first["expected"] != float64(1) || first["allowed"] != float64(21) ||
		first["matched"] != float64(48) || first["expect_source"] != filterclose.ExpectOwnRecord || first["held_since_last"] != float64(0) {
		t.Fatalf("the refusal's line does not place it: %v", first)
	}
	if lines[1]["strategy_id"] != "804" || lines[2]["read"] != float64(500) || lines[2]["duration_ms"] != float64(1500) {
		t.Fatalf("another strategy or outcome was held back: %v", lines)
	}
	if last["strategy_id"] != "802" || last["held_since_last"] != float64(2) {
		t.Fatalf("the line after the period does not count the two held back: %v", last)
	}
}
