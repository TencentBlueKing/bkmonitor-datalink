// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// remembered is how many strategies the reporter holds a line's memory of.
func (reporter *filterCloseJobReporter) remembered() int {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	return len(reporter.last)
}

// lockedBuffer is a log writer the test reads while the closer writes.
type lockedBuffer struct {
	mu  sync.Mutex
	out strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.Write(p)
}

// jobLines is every filtered close job line written so far.
func (b *lockedBuffer) jobLines(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(b.out.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("line %q: %v", raw, err)
		}
		if line["stage"] == filterCloseJobStage {
			lines = append(lines, line)
		}
	}
	return lines
}

// Each filtered close job writes, at its end, one line that places it: the
// strategy, the decision, the job's word, what its real closes answered
// (closed, failed, refused) and how long it ran. While a strategy's word
// stays the same the line is written once a filterCloseJobReportEvery, the
// lines held back counted on the next; a word that changed is written at
// once, and another strategy's line is its own.
func TestEachFilteredCloseJobWritesALineByStrategyHeldWhileItsWordStays(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	stub := &stubControlPlane{bodies: map[string][]string{}}
	plane := httptest.NewServer(stub)
	t.Cleanup(plane.Close)
	var cfg config.Config
	cfg.Redis.StatePrefix = "alarmd-test"
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: plane.URL, JWTSecret: "example-secret"}
	var clock sync.Mutex
	at := time.Unix(1_800_000_000, 0)
	now := func() time.Time { clock.Lock(); defer clock.Unlock(); return at }
	advance := func(d time.Duration) { clock.Lock(); defer clock.Unlock(); at = at.Add(d) }
	client, err := newLinkdManage(cfg, now)
	if err != nil || client == nil {
		t.Fatalf("link client %v (%v)", client, err)
	}
	out := &lockedBuffer{}
	closer, err := newFilterCloser(cfg, client, server.Client(), now, nil, observability.New("alarmd", out))
	if err != nil || closer == nil {
		t.Fatalf("closer %v (%v)", closer, err)
	}
	// One job of strategy: the link holds one alert of it, closed or
	// refused (400) as asked.
	run := func(strategy string, refuse bool) string {
		stub.mu.Lock()
		stub.matches, stub.itemRefused = []string{"alert-" + strategy}, refuse
		stub.mu.Unlock()
		return closer.Absent(context.Background(), filterclose.Scope{TenantID: "tenant-a", EventSourceID: "built_in_bk", StrategyID: strategy},
			filterclose.DecisionAbsent, 3)
	}
	type want struct {
		word                                      string
		closed, failed, refused, held, heldClosed float64
	}
	check := func(step string, wants ...want) {
		t.Helper()
		lines := out.jobLines(t)
		if len(lines) != len(wants) {
			t.Fatalf("%s: lines %v, want %d", step, lines, len(wants))
		}
		for i, w := range wants {
			line := lines[i]
			for _, field := range []string{"tenant", "strategy_id", "decision", "result", "closed", "failed", "refused", "held_since_last",
				"closed_since_last", "duration_ms"} {
				if _, ok := line[field]; !ok {
					t.Fatalf("%s: line %v has no %s", step, line, field)
				}
			}
			if line["level"] != "INFO" || line["tenant"] != "tenant-a" || line["decision"] != filterclose.DecisionAbsent || line["result"] != w.word ||
				line["closed"] != w.closed || line["failed"] != w.failed || line["refused"] != w.refused || line["held_since_last"] != w.held ||
				line["closed_since_last"] != w.heldClosed {
				t.Fatalf("%s: line %d %v, want %+v", step, i, line, w)
			}
		}
	}
	done := want{word: filterclose.OutcomeJobDone, closed: 1}
	if word := run("41", false); word != filterclose.OutcomeJobDone {
		t.Fatalf("word %s", word)
	}
	check("the first job", done)
	advance(time.Minute)
	run("41", false)
	check("the same word within the period", done)
	advance(time.Minute)
	if word := run("41", true); word != filterclose.OutcomeJobDoneRefused {
		t.Fatalf("word %s", word)
	}
	refused := want{word: filterclose.OutcomeJobDoneRefused, failed: 1, refused: 1, held: 1, heldClosed: 1}
	check("the word changed", done, refused)
	advance(time.Minute)
	run("41", false)
	check("changed back", done, refused, done)
	advance(time.Minute)
	run("41", false)
	run("42", false)
	lines := out.jobLines(t)
	if len(lines) != 4 || lines[3]["strategy_id"] != "42" || lines[3]["result"] != filterclose.OutcomeJobDone {
		t.Fatalf("lines %v: want strategy 42's own line", lines)
	}
	advance(filterCloseJobReportEvery)
	run("41", false)
	lines = out.jobLines(t)
	if len(lines) != 5 || lines[4]["strategy_id"] != "41" || lines[4]["result"] != filterclose.OutcomeJobDone || lines[4]["held_since_last"] != float64(1) ||
		lines[4]["closed_since_last"] != float64(1) {
		t.Fatalf("lines %v: want strategy 41's line again after the period, one held that closed one", lines)
	}
}

// What the job lines remember is bounded by the strategy decisions active
// in the last two periods (20 minutes), not by filterCloseReportKeys alone:
// once that many are held, a new one's line drops only those not written
// for two periods. Within them none is dropped, so 4097 strategies active
// within 20 minutes are all kept - the memory grows past
// filterCloseReportKeys while they stay active - and past two periods a new
// line drops them.
func TestTheJobLinesMemoryIsBounded(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	reporter := &filterCloseJobReporter{logger: observability.Discard("alarmd"), now: func() time.Time { return at },
		last: map[string]filterCloseJobWritten{}}
	result := func(strategy int) filterclose.JobResult {
		return filterclose.JobResult{Scope: filterclose.Scope{TenantID: "tenant-a", StrategyID: strconv.Itoa(strategy)}, Word: filterclose.OutcomeJobDone}
	}
	for i := 0; i < filterCloseReportKeys; i++ {
		reporter.report(result(i + 1))
	}
	at = at.Add(2*filterCloseJobReportEvery - time.Second)
	reporter.report(result(filterCloseReportKeys + 1))
	if n := reporter.remembered(); n != filterCloseReportKeys+1 {
		t.Fatalf("remembered %d within two periods, want every one of %d", n, filterCloseReportKeys+1)
	}
	at = at.Add(time.Second)
	reporter.report(result(filterCloseReportKeys + 2))
	if n := reporter.remembered(); n != 2 {
		t.Fatalf("remembered %d after two periods, want the two written since", n)
	}
}

// The alerts closed by the jobs whose lines were held back are carried on
// the strategy's next line (closed_since_last), so summing closed and
// closed_since_last over the lines gives every alert the jobs closed: three
// jobs closing 2, 3 and 4 with the same word write one line, then the
// fourth, after the period, carries the 7 the two held ones closed.
func TestTheLinesHeldBackCarryTheirClosesToTheNext(t *testing.T) {
	out := &lockedBuffer{}
	at := time.Unix(1_800_000_000, 0)
	reporter := &filterCloseJobReporter{logger: observability.New("alarmd", out), now: func() time.Time { return at },
		last: map[string]filterCloseJobWritten{}}
	scope := filterclose.Scope{TenantID: "tenant-a", StrategyID: "41"}
	total := 0
	for _, closed := range []int{2, 3, 4, 5} {
		reporter.report(filterclose.JobResult{Scope: scope, Decision: filterclose.DecisionAbsent, Word: filterclose.OutcomeJobDone, Closed: closed})
		total += closed
		at = at.Add(filterCloseJobReportEvery / 3)
	}
	lines := out.jobLines(t)
	sum := 0.0
	for _, line := range lines {
		sum += line["closed"].(float64) + line["closed_since_last"].(float64)
	}
	if len(lines) != 2 || lines[0]["closed_since_last"] != float64(0) || lines[1]["closed"] != float64(5) ||
		lines[1]["held_since_last"] != float64(2) || lines[1]["closed_since_last"] != float64(7) || sum != float64(total) {
		t.Fatalf("lines %v: want 2 then 5 with the 7 held, summing to %d", lines, total)
	}
}

// Two decisions of one strategy ending with the same word each write their
// own line, each naming its decision: one is not held behind the other's.
// The same decision again within the period is held.
func TestEachDecisionOfAStrategyWritesItsOwnLine(t *testing.T) {
	out := &lockedBuffer{}
	at := time.Unix(1_800_000_000, 0)
	reporter := &filterCloseJobReporter{logger: observability.New("alarmd", out), now: func() time.Time { return at },
		last: map[string]filterCloseJobWritten{}}
	scope := filterclose.Scope{TenantID: "tenant-a", StrategyID: "41"}
	for _, decision := range []string{filterclose.DecisionAbsent, filterclose.DecisionInactive, filterclose.DecisionInactive} {
		reporter.report(filterclose.JobResult{Scope: scope, Decision: decision, Word: filterclose.OutcomeJobDone, Closed: 1})
		at = at.Add(time.Minute)
	}
	lines := out.jobLines(t)
	if len(lines) != 2 || lines[0]["decision"] != filterclose.DecisionAbsent || lines[1]["decision"] != filterclose.DecisionInactive ||
		lines[0]["strategy_id"] != "41" || lines[1]["strategy_id"] != "41" {
		t.Fatalf("lines %v: want one line for each decision of strategy 41", lines)
	}
}
