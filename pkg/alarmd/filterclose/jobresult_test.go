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
	"sync"
	"testing"
)

// jobResults is every JobResult a closer was told.
type jobResults struct {
	mu   sync.Mutex
	seen []JobResult
}

func (r *jobResults) report(result JobResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, result)
}

func (r *jobResults) all() []JobResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]JobResult(nil), r.seen...)
}

// Each job's end is reported once, whatever word ends it, with its
// strategy, its decision, its word and what its real closes answered over
// all its requests: the alerts closed, the items the link answered other
// than 200, and among those the ones it refused and left open. A job that
// sends no real close reports zeros.
func TestEachJobsEndIsReportedWithWhatItsClosesAnswered(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		ahead, closable           int
		closeBudget               int
		word                      string
		closed, failed, refusedOf int
	}{
		{"refused ahead, then closed, over several requests", 6, 10, 5, OutcomeJobDoneRefused, 10, 6, 6},
		{"every alert closed", 0, 3, 0, OutcomeJobDone, 3, 0, 0},
		{"nothing to close", 0, 0, 0, OutcomeNothingMatched, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := refusedAhead(t, tc.ahead, tc.closable, "invalid")
			if tc.ahead+tc.closable == 0 {
				// The record of one alert the link no longer holds: a dry run
				// that matches nothing.
				h.opened(t, "41", "3", nil, fingerprints(0, 1)...)
			}
			h.link.closeBudget = tc.closeBudget
			jobs := &jobResults{}
			h.reportJob = jobs.report
			closer := h.closer(t)
			if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != tc.word {
				t.Fatalf("word %s (stats %v), want %s", word, nonZero(closer.Stats()), tc.word)
			}
			seen := jobs.all()
			if len(seen) != 1 {
				t.Fatalf("job results %+v, want one", seen)
			}
			got := seen[0]
			if got.Scope != testScope || got.Decision != DecisionAbsent || got.Word != tc.word || got.Closed != tc.closed ||
				got.Failed != tc.failed || got.Refused != tc.refusedOf || got.Elapsed <= 0 {
				t.Fatalf("job result %+v, want strategy %s, %s, %s, closed %d, failed %d, refused %d and its time", got,
					testScope.StrategyID, DecisionAbsent, tc.word, tc.closed, tc.failed, tc.refusedOf)
			}
		})
	}
}
