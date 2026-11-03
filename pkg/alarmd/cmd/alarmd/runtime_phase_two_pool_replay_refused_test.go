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

// enterPoolOnAMissingTable runs a minute Query Group through the production
// bundle against a query service that refuses its table, until the third
// refusal puts it in the pool, and returns the next minute.
func enterPoolOnAMissingTable(t *testing.T, h *poolProbeHarness) int64 {
	t.Helper()
	h.backend.missing.Store(true)
	h.backend.latency.Store(200)
	for minute := int64(0); minute <= 2; minute++ {
		requests, events := h.round(minute)
		if len(requests) != 1 || requests[0].answered {
			t.Fatalf("minute %d, a missing table: %+v sent, want one refused query", minute, requests)
		}
		if minute == 2 && (len(events) != 1 || events[0].Event != "entered") {
			t.Fatalf("the third refusal made %+v, want the Query Group entered", events)
		}
	}
	return 3
}

// heldPasses runs passes from at, each a millisecond after the last answer,
// while they complete Slots, and returns every query sent and transition
// made.
func heldPasses(t *testing.T, h *poolProbeHarness, at int64) ([]poolRequest, []observability.QueryCooldownFacts) {
	t.Helper()
	var requests []poolRequest
	var events []observability.QueryCooldownFacts
	for i := 0; ; i++ {
		sent, made, completed := h.passCounting(at)
		requests, events = append(requests, sent...), append(events, made...)
		if completed == 0 {
			return requests, events
		}
		if i == 32 {
			t.Fatalf("still completing Slots after %d passes", i)
		}
		at = h.backend.clock.Load() + 1
	}
}

// A Query Group in the pool because its table does not route in its space is
// refused the same way for any time range, so the first held Slot's replay at
// the cooldown's end is as good a probe as its next on-time round. Refused,
// that replay keeps it in and the Slots still held send nothing: each would
// only be refused again. Once the table routes, the first replay answers, the
// Query Group leaves, and the Slots held after it replay as they always did.
func TestARefusedTableIsProbedByTheFirstReplayAndTheRestWait(t *testing.T) {
	h := newPoolProbeHarnessWith(t, 0, 10*time.Second)
	enterPoolOnAMissingTable(t, h)
	// The cooldown holds every round until it ends; the passes below start
	// after it, with those rounds' Slots held.

	requests, events := heldPasses(t, h, h.afterCooldown())
	if len(requests) != 1 || requests[0].answered || len(events) != 1 || events[0].Event != "extended" ||
		events[0].LastProbe == nil || events[0].LastProbe.Operation != "replay" || events[0].LastProbe.Outcome != observability.QueryCooldownProbeUnavailable {
		t.Fatalf("at the cooldown's end %+v sent and %+v made, want one refused replay deciding the pool and the rest held", requests, events)
	}
	if record := h.poolRecord(); record.Until.IsZero() || record.Reason != "QUERY_TARGET_MISSING" {
		t.Fatalf("the record after the refused replay = %+v, want it in for the missing table", record)
	}
	// The extended cooldown is longer and holds several rounds: still one
	// query, the first held Slot's, and the rest wait.
	requests, events = heldPasses(t, h, h.afterCooldown())
	if len(requests) != 1 || requests[0].answered || len(events) != 1 || events[0].Event != "extended" ||
		events[0].LastProbe == nil || events[0].LastProbe.Operation != "replay" {
		t.Fatalf("at the extended cooldown's end %+v sent and %+v made, want one refused replay and the rest held", requests, events)
	}

	h.backend.missing.Store(false)
	h.backend.latency.Store(1000)
	requests, events = heldPasses(t, h, h.afterCooldown())
	if len(events) != 1 || events[0].Event != "recovered" || events[0].LastProbe == nil ||
		events[0].LastProbe.Operation != "replay" || events[0].LastProbe.Rule != observability.QueryCooldownRuleNoCeiling {
		t.Fatalf("once the table routes the cooldown's end made %+v, want the first replay to take it out with no ceiling", events)
	}
	if len(requests) < 2 {
		t.Fatalf("once the table routes %+v sent, want the deciding replay and the held Slots after it replayed", requests)
	}
	for i, request := range requests {
		if !request.answered {
			t.Fatalf("replay %d after the table routes was not answered: %+v", i, requests)
		}
	}
}

// A replay decides a missing-table pool only on what that reason is about:
// an answer, or the table refused again. A held replay that times out, or
// that the backend cuts, says nothing about whether the table routes - a
// timeout is about the time and the budget the replay had - so it leaves the
// pool exactly as it was, its reason, failures, timeouts and cooldown, and
// the next normal round decides.
func TestAReplayThatTimesOutOrIsCutLeavesAMissingTablePoolToItsNormalRound(t *testing.T) {
	for _, test := range []struct {
		name  string
		setUp func(h *poolProbeHarness)
	}{
		{"timed out", func(h *poolProbeHarness) { h.backend.latency.Store((60 * time.Second).Milliseconds()) }},
		{"cut by the backend", func(h *poolProbeHarness) {
			h.backend.cut.Store(backendCut.Milliseconds())
			h.backend.latency.Store((backendCut + time.Second).Milliseconds())
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPoolProbeHarnessWith(t, 0, 10*time.Second)
			enterPoolOnAMissingTable(t, h)
			before := h.poolRecord()
			h.backend.missing.Store(false)
			test.setUp(h)
			requests, events := heldPasses(t, h, h.afterCooldown())
			if len(requests) == 0 || len(events) != 0 {
				t.Fatalf("held replays %s: %+v sent and %+v made, want them run and no pool transition", test.name, requests, events)
			}
			for i, request := range requests {
				if request.answered {
					t.Fatalf("held replay %d was answered: %+v", i, requests)
				}
			}
			after := h.poolRecord()
			if !after.Until.Equal(before.Until) || after.Reason != before.Reason || after.Failures != before.Failures ||
				after.Timeouts != before.Timeouts || after.Reason != "QUERY_TARGET_MISSING" {
				t.Fatalf("the record after held replays %s = %+v, want it as before them: %+v", test.name, after, before)
			}

			// The next normal round decides: the table still refused, it stays.
			h.backend.cut.Store(0)
			h.backend.missing.Store(true)
			h.backend.latency.Store(200)
			minute := (h.backend.clock.Load()/1000-h.base)/60 + 1
			requests, events = h.pass((h.base+minute*60)*1000 + (h.readWait() + poolPassSlack).Milliseconds())
			if len(requests) != 1 || len(events) != 1 || events[0].Event != "extended" || events[0].LastProbe == nil ||
				events[0].LastProbe.Operation != "normal" {
				t.Fatalf("the next normal round %+v sent and %+v made, want it to decide the pool", requests, events)
			}
		})
	}
}
