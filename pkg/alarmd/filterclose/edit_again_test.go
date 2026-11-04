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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
)

// openedAt is an alert as opened returns it, opened at created.
func openedAt(alert *fakeAlert, created time.Time) *fakeAlert {
	alert.created = created
	return alert
}

// Each job of an edit close is run again within the same Edit while it has
// more and its own last run closed something: a close the
// link answers busy for one of two alerts closes the other, and the run
// again closes the one left, whichever of the edit's jobs the alerts fall
// to. Before, the Edit kept its jobs' words to itself and the strategy
// waited a round for the rest.
func TestEveryEditJobIsRunAgainWithinTheEditWhileItClosesSome(t *testing.T) {
	old, current := factsOf("m4"), factsOf("m5")
	undecided := factsOf("m5")
	undecided.DimensionDigest = ""
	removedDim := func() map[string]string { return map[string]string{"bk_target_ip": "192.0.2.1", "removed_dim": "x"} }
	// Opened before the record vouches (its start plus settle).
	before := fakeRecordStart.Add(-time.Hour)
	for _, tc := range []struct {
		name   string
		alerts []*fakeAlert
		record bool
	}{
		{"a bucket closed by its filter", []*fakeAlert{
			opened("j1", "4", old, false, "critical", nil), opened("j2", "4", old, false, "critical", nil)}, true},
		{"a bucket without a record, alert by alert", []*fakeAlert{
			opened("j1", "4", old, false, "critical", nil), opened("j2", "4", old, false, "critical", nil)}, false},
		{"a bucket's alerts opened before the record's start, alert by alert", []*fakeAlert{
			openedAt(opened("j1", "4", old, false, "critical", nil), before),
			openedAt(opened("j2", "4", old, false, "critical", nil), before)}, true},
		{"alerts opened without a digest, alert by alert", []*fakeAlert{
			opened("j1", "3", nil, false, "critical", removedDim()), opened("j2", "3", nil, false, "critical", removedDim())}, false},
		{"a bucket only each alert's dimensions decide, alert by alert", []*fakeAlert{
			opened("j1", "4", undecided, false, "critical", removedDim()),
			opened("j2", "4", undecided, false, "critical", removedDim())}, false},
		{"the level filter over alerts the record vouches for", []*fakeAlert{
			opened("j1", "4", current, false, "info", nil), opened("j2", "4", current, false, "info", nil)}, true},
		{"the level filter's alerts opened before the record's start, alert by alert", []*fakeAlert{
			openedAt(opened("j1", "4", current, false, "info", nil), before),
			openedAt(opened("j2", "4", current, false, "info", nil), before)}, true},
		{"the level filter without a record, alert by alert", []*fakeAlert{
			opened("j1", "4", current, false, "info", nil), opened("j2", "4", current, false, "info", nil)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			for _, alert := range tc.alerts {
				h.link.add(alert)
			}
			if tc.record {
				roster(t, h)
			}
			h.link.itemFault = func(id string, n int) string {
				if id == "j2" && n == 0 {
					return "busy"
				}
				return ""
			}
			closer := h.closer(t)
			if word, _ := closer.Edit(testwait.Context(t), EditInput{Scope: testScope, Plan: currentPlan()}); word != "" {
				t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
			}
			if j1, j2 := h.link.alert("j1"), h.link.alert("j2"); j1.closes != 1 || j2.closes != 1 || j2.attempts != 2 {
				t.Fatalf("j1 closed %d, j2 closed %d in %d attempts, want both closed in this Edit, j2 at its second (stats %v)",
					j1.closes, j2.closes, j2.attempts, nonZero(closer.Stats()))
			}
			if again := closer.Stats()[OutcomeJobAgain]; again != 1 {
				t.Fatalf("job_again %d, want the one run again (stats %v)", again, nonZero(closer.Stats()))
			}
		})
	}
}

// closingMeanwhile is the closer's link with another job of the same
// Closer closing alerts while the first real close of strategy 41 is in
// flight: what the process closed grows during that job's run though the
// job itself closes nothing.
type closingMeanwhile struct {
	Link
	meanwhile func()
	done      bool
}

func (link *closingMeanwhile) CloseByFilter(ctx context.Context, command []byte, tenantID string, filter json.RawMessage,
	dryRun bool) (linkdmanage.FilterPage, error) {
	if !dryRun && !link.done && strings.Contains(string(filter), `"41"`) {
		link.done = true
		link.meanwhile()
	}
	return link.Link.CloseByFilter(ctx, command, tenantID, filter, dryRun)
}

// A job that closed nothing is not run again within the Edit though
// another job of the same Closer closed alerts while it ran: progress is
// the job's own, not what the process closed meanwhile (the absent, edit
// and scope closes share one Closer, and the effective-time close runs on
// another goroutine).
func TestAnEditJobThatClosedNothingIsNotRunAgainForWhatAnotherClosedMeanwhile(t *testing.T) {
	h := newHarness(t)
	h.link.add(opened("j1", "3", nil, false, "critical", map[string]string{"bk_target_ip": "192.0.2.1", "removed_dim": "x"}))
	h.link.add(&fakeAlert{id: "other", fingerprint: "f1", labels: map[string]string{"strategy_id": "42"}})
	h.link.itemFault = func(id string, n int) string {
		if id == "j1" {
			return "busy"
		}
		return ""
	}
	closer := h.closer(t)
	other := testScope
	other.StrategyID = "42"
	closer.link = &closingMeanwhile{Link: closer.link, meanwhile: func() {
		if word := closer.Fingerprints(context.Background(), other, []string{"f1"}, "target_out_of_scope", 7, fakeDecidedAt); word != OutcomeJobDone {
			t.Errorf("the other job's word %s, want job_done", word)
		}
	}}
	closer.Edit(testwait.Context(t), EditInput{Scope: testScope, Plan: currentPlan()})
	if h.link.alert("other").closes != 1 {
		t.Fatalf("the other job closed %d, want 1: nothing closed meanwhile (stats %v)", h.link.alert("other").closes, nonZero(closer.Stats()))
	}
	if j1 := h.link.alert("j1"); j1.attempts != 1 || closer.Stats()[OutcomeJobAgain] != 0 {
		t.Fatalf("j1 attempted %d times, job_again %d: want one run, not run again (stats %v)",
			j1.attempts, closer.Stats()[OutcomeJobAgain], nonZero(closer.Stats()))
	}
}
