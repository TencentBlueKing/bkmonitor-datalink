// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func (rig *diagnosisRig) summary(t *testing.T, query string) (int, DiagnosisSummaryResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	rig.handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/diagnose?"+query, nil))
	var body DiagnosisSummaryResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, body
}

// The first screen's count of strategies is the diagnosis's: the universe's
// size, and by verdict what the pages of a diagnosis sum to, read the same
// way at the same moment - so the page and the CLI cannot say two things.
// The Query Groups beside it are the view's own count, another unit: eleven
// strategies made up into three Query Groups read as eleven and three.
func TestTheFirstScreenCountIsTheDiagnosisPagesSummed(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"4101", "4102", "4103", "4109"}
	for i := 1; i <= 7; i++ {
		rig.universe = append(rig.universe, strconv.Itoa(5000+i))
	}
	summed := map[StateWord]int{}
	for cursor := ""; ; {
		body := rig.page(t, cursor, 3)
		for word, n := range body.Page.ByVerdict {
			summed[word] += n
		}
		if cursor = body.NextCursor; cursor == "" {
			break
		}
	}
	code, body := rig.summary(t, "summary=1")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	got := body.Summary
	if got.Strategies != 11 || body.Universe.Count != 11 || body.Universe.Status != "ok" {
		t.Fatalf("summary %+v universe %+v, want eleven strategies", got, body.Universe)
	}
	total := 0
	for _, word := range DiagnosisVerdicts() {
		if got.ByVerdict[word] != summed[word] {
			t.Errorf("%s: summary %d, pages %d", word, got.ByVerdict[word], summed[word])
		}
		total += got.ByVerdict[word]
		if body.Words.State[word] == "" {
			t.Errorf("verdict %s has no rendering in the words the summary carries", word)
		}
	}
	if total != 11 || got.ByVerdict[DiagnosisUnknown] != 7 {
		t.Fatalf("by verdict %v, want eleven with the seven unlisted unknown", got.ByVerdict)
	}
	if got.QueryGroups == nil || *got.QueryGroups != 3 {
		t.Fatalf("query groups %v, want the view's three - not the strategies' eleven", got.QueryGroups)
	}
	if len(body.Verdicts) != len(DiagnosisVerdicts()) {
		t.Fatalf("verdicts %v, want the closed list in its order", body.Verdicts)
	}
}

// A summary counts the whole universe, so a cursor or a limit beside it is
// refused rather than read as a page; and a universe that cannot be read is
// said by name with nothing counted, never as no strategies.
func TestAFirstScreenCountTakesNoPageAndNamesAnUnreadableUniverse(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"4101", "4102", "4103"}
	// A cursor a page would take, so the refusal is the summary's and not
	// the cursor's own.
	cursor := rig.page(t, "", 1).NextCursor
	if cursor == "" {
		t.Fatal("setup: no cursor from a one-row page")
	}
	for _, query := range []string{"summary=1&limit=3", "summary=1&cursor=" + url.QueryEscape(cursor), "summary=true"} {
		if code, _ := rig.summary(t, query); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want refused", query, code)
		}
	}
	rig.universeErr = errors.New("SOURCE_UNREADABLE")
	code, body := rig.summary(t, "summary=1")
	if code != http.StatusOK || body.Universe.Status != "unreadable" || body.Universe.Reason != "SOURCE_UNREADABLE" ||
		body.Summary.Strategies != 0 || len(body.Summary.ByVerdict) != 0 {
		t.Fatalf("status %d body %+v, want the universe named unreadable and nothing counted", code, body)
	}
}

// A first-screen count is read for itself and kept by nobody: a page asks
// on every refresh, and a diagnosis somebody is paging through keeps its
// read however many counts are asked between its pages.
func TestAFirstScreenCountDoesNotPushADiagnosisOutOfTheCache(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	for i := 1; i <= 7; i++ {
		rig.universe = append(rig.universe, strconv.Itoa(5000+i))
	}
	first := rig.page(t, "", 3)
	reads := rig.universeReads
	// Each count past the last one's freshness, so each is a read of its
	// own - and all of them well inside the paged diagnosis's own TTL.
	for range 2 * DiagnosisCacheEntries {
		rig.clock = rig.clock.Add(DiagnosisSummaryFreshFor)
		if code, _ := rig.summary(t, "summary=1"); code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
	}
	if rig.universeReads != reads+2*DiagnosisCacheEntries {
		t.Fatalf("universe read %d times for %d counts, want one each", rig.universeReads-reads, 2*DiagnosisCacheEntries)
	}
	next := rig.page(t, first.NextCursor, 3)
	if next.SnapshotReread || rig.universeReads != reads+2*DiagnosisCacheEntries {
		t.Fatalf("the paged diagnosis reread (%v, %d reads) after the counts, want its own read kept", next.SnapshotReread,
			rig.universeReads-reads)
	}
}

// A count answers every page that asks while it is fresh, read once however
// many ask, and is read again after DiagnosisSummaryFreshFor. One that could
// not be read is not kept: the next ask reads again.
func TestAFirstScreenCountIsReadOnceWhileFresh(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"4101", "4102"}
	for range 3 {
		if code, _ := rig.summary(t, "summary=1"); code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
	}
	if rig.universeReads != 1 {
		t.Fatalf("universe read %d times for three counts within %v, want once", rig.universeReads, DiagnosisSummaryFreshFor)
	}
	rig.universe = append(rig.universe, "4103")
	rig.clock = rig.clock.Add(DiagnosisSummaryFreshFor)
	if _, body := rig.summary(t, "summary=1"); rig.universeReads != 2 || body.Summary.Strategies != 3 {
		t.Fatalf("after %v: %d reads, %d strategies; want a new read counting three", DiagnosisSummaryFreshFor, rig.universeReads,
			body.Summary.Strategies)
	}
	rig.universeErr = errors.New("SOURCE_UNREADABLE")
	rig.clock = rig.clock.Add(DiagnosisSummaryFreshFor)
	rig.summary(t, "summary=1")
	rig.universeErr = nil
	if _, body := rig.summary(t, "summary=1"); rig.universeReads != 4 || body.Universe.Status != "ok" {
		t.Fatalf("after an unreadable count: %d reads, universe %+v; want it read again and answered", rig.universeReads, body.Universe)
	}
}

// The first-screen count carries the replicas' latest overdue episodes, read
// from their snapshots with the view the diagnosis is decided on, the
// latest-ended first.
func TestTheFirstScreenCountCarriesTheOverdueEpisodes(t *testing.T) {
	snapshots := healthySnapshots()
	ended := func(queryGroup string, clear time.Duration, hold int64) OverdueEpisode {
		return OverdueEpisode{OverdueObject: OverdueObject{QueryGroup: queryGroup, IntervalSeconds: 60}, Replica: snapshots[0].Replica,
			Onset: now.Add(clear - time.Minute), Clear: now.Add(clear), ReadHoldMillis: hold, ReadHoldKnown: true}
	}
	snapshots[0].OverdueEpisodes = []OverdueEpisode{ended("qg-old", -2*time.Hour, 0), ended("qg-held", -time.Hour, 308_000)}
	rig := newDiagnosisRigFrom(t, diagnosisFacts(), nil, snapshots)
	rig.universe = []string{"4101"}
	code, body := rig.summary(t, "summary=1")
	if code != http.StatusOK || len(body.OverdueEpisodes) != 2 || body.OverdueEpisodes[0].QueryGroup != "qg-held" ||
		body.OverdueEpisodes[0].HoldClass() != "positive" {
		t.Fatalf("status %d episodes %+v, want qg-held (held) then qg-old", code, body.OverdueEpisodes)
	}
}
