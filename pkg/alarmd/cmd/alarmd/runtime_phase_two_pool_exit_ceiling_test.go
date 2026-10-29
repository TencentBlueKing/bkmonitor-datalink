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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// slowQueryRuns are a query's answered run times as one production Query
// Group showed them, just under its query service's 30 s storage timeout.
var slowQueryRuns = []time.Duration{28100 * time.Millisecond, 29900 * time.Millisecond, 28600 * time.Millisecond,
	29100 * time.Millisecond, 28400 * time.Millisecond, 29500 * time.Millisecond, 28900 * time.Millisecond, 29300 * time.Millisecond}

// backendCut is that storage timeout: the query service answers unavailable
// when its storage client gives up, 30 s after the query was sent.
const backendCut = 30 * time.Second

// enterPoolOnTheBackendsCut runs a minute Query Group through the production
// bundle on slowQueryRuns, then three rounds the backend cuts, and returns the
// next minute and the entry.
func enterPoolOnTheBackendsCut(t *testing.T, h *poolProbeHarness) (int64, observability.QueryCooldownFacts) {
	t.Helper()
	if h.normalBudget() <= backendCut+time.Second {
		t.Fatalf("a normal round has %s, want more than the backend's %s cut so the backend cuts before alarmd's deadline", h.normalBudget(), backendCut)
	}
	h.backend.cut.Store(backendCut.Milliseconds())
	minute := int64(0)
	for _, run := range slowQueryRuns {
		h.backend.latency.Store(run.Milliseconds())
		if requests, events := h.round(minute); len(requests) != 1 || !requests[0].answered || len(events) != 0 {
			t.Fatalf("minute %d, a %s answer: %+v sent and %+v made, want one answered query outside the pool", minute, run, requests, events)
		}
		minute++
	}
	h.backend.latency.Store((backendCut + time.Second).Milliseconds())
	var entry observability.QueryCooldownFacts
	for cut := 0; cut < 3; cut++ {
		requests, events := h.round(minute)
		minute++
		if len(requests) != 1 || requests[0].answered {
			t.Fatalf("cut %d: %+v sent, want one query the backend cut", cut, requests)
		}
		if cut == 2 {
			if len(events) != 1 || events[0].Event != "entered" {
				t.Fatalf("the third cut made %+v, want the Query Group entered", events)
			}
			entry = events[0]
		}
	}
	return minute, entry
}

// A query that runs 28.1 to 29.9 s against a backend that cuts it at 30.0 s
// went in and out of the pool hundreds of times: every exit an answer that
// came back under the cut, every re-entry the next rounds that did not. The
// entry now records the cut its failures showed. A probe answering at 29.9 s,
// within the query's own spread of the cut, keeps the Query Group in and
// says so; one answering at 24.9 s, clear of the cut by more than that
// spread, takes it out and says so. Both comparisons are kept on the record a
// restart or the next owner reads, and on the line object.get shows.
func TestASlowQueryLeavesThePoolOnlyClearOfItsBackendsCut(t *testing.T) {
	h := newPoolProbeHarnessWith(t, 0, 10*time.Second)
	_, entry := enterPoolOnTheBackendsCut(t, h)
	if entry.CeilingMillis != backendCut.Milliseconds() || entry.RunSpreadMillis != 1800 || entry.Reentries != 0 {
		t.Fatalf("the entry = %+v, want the %s cut and the runs' 1.8 s spread recorded", entry, backendCut)
	}

	// Where the deciding round falls - at the cooldown's end or the round
	// after - depends on the wall-clock minute the case runs in; either way
	// it is a normal round, and that is what decides.
	h.backend.latency.Store(29900)
	_, decider, event, atEnd := h.decide(0)
	probe := event.LastProbe
	if probe == nil || !decider.answered || probe.Operation != "normal" || event.Event != "extended" ||
		probe.Rule != observability.QueryCooldownRuleNearCeiling || probe.ElapsedMillis != 29900 ||
		probe.CeilingMillis != 30000 || probe.RunSpreadMillis != 1800 {
		t.Fatalf("a probe answering at 29.9 s: decided at the end %t, %+v made %+v (probe %+v), want it held near the ceiling", atEnd, decider, event, probe)
	}

	h.backend.latency.Store(24900)
	_, decider, event, atEnd = h.decide(0)
	probe = event.LastProbe
	// The 29.9 s probe joined the runs and the 28.1 s one aged out: 28.4 to
	// 29.9 s.
	if probe == nil || !decider.answered || probe.Operation != "normal" || event.Event != "recovered" ||
		probe.Rule != observability.QueryCooldownRuleUnderCeiling || probe.ElapsedMillis != 24900 ||
		probe.CeilingMillis != 30000 || probe.RunSpreadMillis != 1500 {
		t.Fatalf("a probe answering at 24.9 s: decided at the end %t, %+v made %+v (probe %+v), want it out under the ceiling", atEnd, decider, event, probe)
	}
	if len(event.Probes) != 2 || event.Probes[0].Rule != observability.QueryCooldownRuleNearCeiling ||
		event.Probes[1].Rule != observability.QueryCooldownRuleUnderCeiling {
		t.Fatalf("the probe history on the line = %+v, want the held probe then the one that let it out", event.Probes)
	}
	record := h.poolRecord()
	if !record.Until.IsZero() || record.CeilingMillis != 30000 || len(record.Probes) != 2 || *record.LastProbe != *probe {
		t.Fatalf("the record after the exit = %+v, want it out with the ceiling and both probes kept", record)
	}
}

// A backend whose limit was raised answers past the cut the Query Group was
// held to. That answer takes it out and drops the ceiling: holding it to a
// cut that is no longer there would keep it in the pool for good.
func TestAnAnswerPastTheCutTakesTheQueryGroupOutAndDropsTheCeiling(t *testing.T) {
	h := newPoolProbeHarnessWith(t, 0, 10*time.Second)
	enterPoolOnTheBackendsCut(t, h)
	h.backend.cut.Store(0)
	h.backend.latency.Store(35000)
	_, decider, event, _ := h.decide(0)
	probe := event.LastProbe
	if probe == nil || !decider.answered || probe.Operation != "normal" || event.Event != "recovered" ||
		probe.Rule != observability.QueryCooldownRuleAboveCeiling || probe.CeilingMillis != 30000 {
		t.Fatalf("a probe answering at 35 s past a 30 s cut: %+v made %+v (probe %+v), want it out above the ceiling", decider, event, probe)
	}
	if record := h.poolRecord(); record.CeilingMillis != 0 || !record.Until.IsZero() {
		t.Fatalf("the record after an answer past the cut = %+v, want it out and the ceiling dropped", record)
	}
}

// reentryRuns are answered run times like one production Query Group's
// between two of its pool memberships on 2026-10-13: 20.0 to 29.19 s, a 9.19 s
// spread, under the same 30 s cut.
var reentryRuns = []time.Duration{24100 * time.Millisecond, 20000 * time.Millisecond, 27300 * time.Millisecond,
	22800 * time.Millisecond, 29190 * time.Millisecond, 25600 * time.Millisecond, 21400 * time.Millisecond, 26900 * time.Millisecond}

// reenterAfterAnAnswer takes a Query Group in the pool out on an answer clear
// of the cut by its spread, runs it outside on reentryRuns, then three rounds
// the backend cuts, and returns the re-entry.
func reenterAfterAnAnswer(t *testing.T, h *poolProbeHarness) observability.QueryCooldownFacts {
	t.Helper()
	h.backend.latency.Store(16808)
	_, _, event, _ := h.decide(0)
	if event.Event != "recovered" || event.LastProbe == nil || event.LastProbe.Rule != observability.QueryCooldownRuleUnderCeiling {
		t.Fatalf("a first entry's probe at 16.8 s against a 30 s cut made %+v, want it out under the ceiling", event)
	}
	minute := (h.backend.clock.Load()/1000-h.base)/60 + 1
	for _, run := range reentryRuns {
		h.backend.latency.Store(run.Milliseconds())
		if requests, events := h.round(minute); len(requests) != 1 || !requests[0].answered || len(events) != 0 {
			t.Fatalf("minute %d, a %s answer outside the pool: %+v sent and %+v made, want one answered query", minute, run, requests, events)
		}
		minute++
	}
	h.backend.latency.Store((backendCut + time.Second).Milliseconds())
	for cut := 0; cut < 3; cut++ {
		requests, events := h.round(minute)
		minute++
		if len(requests) != 1 || requests[0].answered {
			t.Fatalf("cut %d after leaving: %+v sent, want one query the backend cut", cut, requests)
		}
		if cut == 2 {
			if len(events) != 1 || events[0].Event != "reentered" {
				t.Fatalf("the third cut after leaving on an answer made %+v, want the Query Group re-entered", events)
			}
			return events[0]
		}
	}
	return observability.QueryCooldownFacts{}
}

// A Query Group that left on an answer clear of its cut and came back within
// the hour has shown that such an answer is not evidence for it. On
// 2026-10-13 one production Query Group left on a 17.03 s answer whose 9.19 s
// spread cleared the 30 s cut, and was back in an hour later, the fourth time
// that day. The same probe on a re-entry now keeps it in and names why, and
// the record a restart or the next owner reads carries that; an answer past
// the cut - the backend changed - still takes it out.
func TestAReenteredQueryGroupLeavesOnlyOnEvidence(t *testing.T) {
	h := newPoolProbeHarnessWith(t, 0, 10*time.Second)
	enterPoolOnTheBackendsCut(t, h)
	reentry := reenterAfterAnAnswer(t, h)
	if reentry.CeilingMillis != backendCut.Milliseconds() || reentry.RunSpreadMillis != 9190 || reentry.Reentries != 1 || reentry.Cuts != 3 {
		t.Fatalf("the re-entry = %+v, want the 30 s cut, the 9.19 s spread, one re-entry and its three cuts", reentry)
	}

	h.backend.latency.Store(17030)
	_, decider, event, atEnd := h.decide(0)
	probe := event.LastProbe
	if probe == nil || !decider.answered || probe.Operation != "normal" || event.Event != "extended" || event.Cuts != 3 ||
		probe.Rule != observability.QueryCooldownRuleHeldAfterReentry || probe.ElapsedMillis != 17030 ||
		probe.CeilingMillis != 30000 || probe.RunSpreadMillis != 9190 {
		t.Fatalf("a re-entered probe at 17.03 s: decided at the end %t, %+v made %+v (probe %+v), want it held after the re-entry", atEnd, decider, event, probe)
	}
	if record := h.poolRecord(); record.Until.IsZero() || record.Reentries != 1 || record.ExitReason != "recovered" ||
		record.LastProbe == nil || record.LastProbe.Rule != observability.QueryCooldownRuleHeldAfterReentry {
		t.Fatalf("the record after the held probe = %+v, want it in with the re-entry and the rule that held it", record)
	}

	h.backend.cut.Store(0)
	h.backend.latency.Store(35000)
	_, decider, event, _ = h.decide(0)
	probe = event.LastProbe
	if probe == nil || !decider.answered || event.Event != "recovered" || probe.Rule != observability.QueryCooldownRuleAboveCeiling {
		t.Fatalf("a re-entered probe answering at 35 s past the 30 s cut: %+v made %+v (probe %+v), want it out above the ceiling", decider, event, probe)
	}
	if record := h.poolRecord(); !record.Until.IsZero() || record.CeilingMillis != 0 {
		t.Fatalf("the record after the answer past the cut = %+v, want it out and the ceiling dropped", record)
	}
}
