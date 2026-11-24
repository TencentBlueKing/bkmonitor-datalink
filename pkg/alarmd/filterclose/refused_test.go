// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"testing"
)

// refusedAhead is strategy 41 at version 3 with refused alerts whose ids
// sort first and closable ones after them, alarmd's record of all of
// them, and a link that refuses the first ones' items with fault.
func refusedAhead(t *testing.T, refused, closable int, fault string) *harness {
	t.Helper()
	h := newHarness(t)
	n := refused + closable
	for i := 0; i < n; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, n)...)
	first := map[string]bool{}
	for i := 0; i < refused; i++ {
		first[alertID(i)] = true
	}
	h.link.itemFault = func(id string, _ int) string {
		if first[id] {
			return fault
		}
		return ""
	}
	return h
}

// attemptedOnce fails unless each of the first n alerts was attempted
// exactly once: a refused id is not named again within the run.
func attemptedOnce(t *testing.T, h *harness, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if attempts := h.link.alert(alertID(i)).attempts; attempts != 1 {
			t.Fatalf("%s attempted %d times, want once", alertID(i), attempts)
		}
	}
}

// Alerts the link refuses and leaves open whose ids sort first no longer
// hold back the ones after them: a real close names at most
// closeIDsPerRequest ids, the first ones the dry run read, so with one
// more than that refused the first close closes nothing. The run goes on
// with the ids it has not sent and closes the rest in the same run, each
// refused id named once; the job ends refused, its record kept.
func TestRefusedAlertsAheadDoNotHoldBackTheRest(t *testing.T) {
	const refused, closable = closeIDsPerRequest + 1, 10
	h := refusedAhead(t, refused, closable, "invalid")
	seen := &requests{}
	h.observe = seen.observe
	closer := h.closer(t)
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeJobDoneRefused {
		t.Fatalf("word %s (stats %v), want %s", word, nonZero(closer.Stats()), OutcomeJobDoneRefused)
	}
	for i := refused; i < refused+closable; i++ {
		if closes := h.link.alert(alertID(i)).closes; closes != 1 {
			t.Fatalf("%s closed %d times, want once in the run", alertID(i), closes)
		}
	}
	attemptedOnce(t, h, refused)
	if dry, closes := seen.dryRuns(), seen.closes(); len(dry) != 2 || len(closes) != 2 {
		t.Fatalf("dry runs %v, closes %v: want two of each", dry, closes)
	}
	if stats := closer.Stats(); stats[OutcomeItemInvalidCommand] != refused || stats[OutcomeItemClosed] != closable ||
		stats[OutcomeJobIncomplete] != 0 {
		t.Fatalf("stats %v", nonZero(stats))
	}
}

// Every way the link refuses an item and leaves it open is skipped the
// same way, where the link's own budget per request (closeBudget) spends
// a request on refused items alone.
func TestEachRefusalAheadIsSkippedWithinTheRun(t *testing.T) {
	for _, fault := range []string{"invalid", "foreign", "unpublished", "conflict_other", "unprocessable"} {
		t.Run(fault, func(t *testing.T) {
			const refused, closable = 6, 10
			h := refusedAhead(t, refused, closable, fault)
			h.link.closeBudget = 5
			closer := h.closer(t)
			if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeJobDoneRefused {
				t.Fatalf("word %s (stats %v), want %s", word, nonZero(closer.Stats()), OutcomeJobDoneRefused)
			}
			if closed := closedAlerts(t, h, refused+closable); closed != closable {
				t.Fatalf("closed %d, want the %d after the refused ones", closed, closable)
			}
			attemptedOnce(t, h, refused)
		})
	}
}

// A job whose every alert the link refuses still ends: refused where the
// dry run read every match, incomplete where the link has more past what
// its dry run read and all of that was refused. Neither sends a request
// once nothing is left to send, nor names a refused id twice, nor runs
// into the round's time.
func TestAJobWhoseEveryAlertIsRefusedEnds(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		// closeBudget, dryBudget and closeHasMore set the fake link's.
		closeBudget, dryBudget int
		closeHasMore           bool
		word                   string
		dryRuns, closes        int
	}{
		{"refused over several requests", 12, 5, 0, false, OutcomeJobDoneRefused, 3, 3},
		{"the last close says has_more", 10, 0, 0, true, OutcomeJobDoneRefused, 2, 1},
		{"the dry run has more past the refused", 20, 0, 10, false, OutcomeJobIncomplete, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := refusedAhead(t, tc.n, 0, "invalid")
			h.link.closeBudget, h.link.dryBudget, h.link.closeHasMore = tc.closeBudget, tc.dryBudget, tc.closeHasMore
			seen := &requests{}
			h.observe = seen.observe
			closer := h.closerWithBudget(t, slowLinkRequest)
			ctx, cancel := context.WithTimeout(context.Background(), slowLinkRound) // fixed-wait: the round's time is the bound under test
			defer cancel()
			word := closer.Absent(ctx, testScope, DecisionAbsent, 3)
			if dry, closes := seen.dryRuns(), seen.closes(); word != tc.word || len(dry) != tc.dryRuns || len(closes) != tc.closes {
				t.Fatalf("word %s, dry runs %v, closes %v (stats %v): want %s after %d and %d", word, dry, closes,
					nonZero(closer.Stats()), tc.word, tc.dryRuns, tc.closes)
			}
			if stats := closer.Stats(); stats[OutcomeRoundSpent] != 0 || stats[OutcomeItemClosed] != 0 {
				t.Fatalf("stats %v: want no round spent and nothing closed", nonZero(stats))
			}
			attempted := tc.n
			if tc.dryBudget > 0 {
				attempted = tc.dryBudget
			}
			attemptedOnce(t, h, attempted)
		})
	}
}

// A dry run that read more ids than one real close names, whose close's
// answer is lost, keeps to retry by id only the ids that close named: the
// rest were never sent, are still active, and the next run's filter finds
// them again.
func TestALostAnswerPastOneRequestKeepsOnlyItsBatch(t *testing.T) {
	const n = closeIDsPerRequest + 4
	h := newHarness(t)
	for i := 0; i < n; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, n)...)
	h.link.loseAnswer = func(n int) bool { return n == 1 }
	closer := h.closer(t)
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeLinkFailed {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	if stats, size := closer.Stats(), closer.PendingSize(context.Background()); stats[OutcomeItemPending] != closeIDsPerRequest ||
		size != closeIDsPerRequest {
		t.Fatalf("stats %v, pending %d: want the %d ids the close named, not the %d the dry run read", nonZero(stats), size,
			closeIDsPerRequest, n)
	}
	for i := closeIDsPerRequest; i < n; i++ {
		if alert := h.link.alert(alertID(i)); alert.status != "active" || alert.attempts != 0 {
			t.Fatalf("%s %s after %d attempts: a close that did not name it", alertID(i), alert.status, alert.attempts)
		}
	}
}
