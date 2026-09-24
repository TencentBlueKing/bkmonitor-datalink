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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// diagnosisFacts is the standing fixture's strategies, plus the ids a
// diagnosis must still answer: one the catalog never heard of.
func diagnosisFacts() map[string]StrategyLookupFacts {
	pub := StrategyPublication{SnapshotRevision: "s1", Epoch: 7}
	return map[string]StrategyLookupFacts{
		"4101": {Available: true, Found: true, Publication: pub, Plans: plans(planA, planB),
			Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "ACCEPTED"}}},
		"4102": {Available: true, Found: true, Publication: pub, Dispositions: []StrategyDisposition{
			{Scope: "PLAN", Disposition: "UNSUPPORTED_PHASE2_CAPABILITY", Reason: "ALGORITHM_NOT_MIGRATED", FieldPath: "items[0].algorithms[0]"}}},
		"4103": {Available: true, Found: true, Publication: pub, Plans: plans(planA), Dispositions: []StrategyDisposition{
			{Scope: "LEVEL", LevelID: 2, Disposition: "CONFIG_REJECTED", Reason: "LEVEL_INVALID", FieldPath: "items[0].algorithms[1].config"},
			{Scope: "LEVEL", LevelID: 1, Disposition: "ACCEPTED"}}},
		"4109": {Available: true, Found: true, Publication: pub, Dispositions: []StrategyDisposition{
			{Scope: "PLAN", Disposition: "SOURCE_INCOMPLETE", Reason: "EFFECTIVE_TIME_SNAPSHOT_UNAVAILABLE", FieldPath: "effective_time_snapshot"}}},
	}
}

type diagnosisRig struct {
	handler       http.Handler
	universe      []string
	universeErr   error
	universeReads int
	clock         time.Time
	// warmer is built from the same readers the handler answers from.
	warmer *DiagnosisWarmer
}

func newDiagnosisRig(t *testing.T, facts map[string]StrategyLookupFacts, progress ProgressReader) *diagnosisRig {
	t.Helper()
	return newDiagnosisRigWith(t, facts, progress, nil)
}

// newDiagnosisRigWith lets a test shape the one anomaly row the fleet holds
// (4101's object on pod-b) before the service is built.
func newDiagnosisRigWith(t *testing.T, facts map[string]StrategyLookupFacts, progress ProgressReader, shape func(*Anomaly)) *diagnosisRig {
	t.Helper()
	rig := &diagnosisRig{clock: now}
	snapshots := healthySnapshots()
	snapshots[0].Owned, snapshots[0].Determined = 2, 2
	snapshots[0].OwnedObjects = []string{"qg-4101-a", "qg-other"}
	snapshots[1].Owned, snapshots[1].Determined = 1, 1
	snapshots[1].OwnedObjects = []string{"qg-4101-b"}
	row := anomaly("qg-4101-b")
	row.Replica = "pod-b"
	if shape != nil {
		shape(&row)
	}
	snapshots[1].Anomalies = []Anomaly{row}
	snapshots[1].TotalAnomalies = 1
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 3, Known: true, IDs: []string{"qg-4101-a", "qg-4101-b", "qg-other"}}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	lookup := func(id string) StrategyLookupFacts {
		if f, ok := facts[id]; ok {
			return f
		}
		return StrategyLookupFacts{Available: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7}}
	}
	if facts == nil {
		lookup = func(string) StrategyLookupFacts { return StrategyLookupFacts{} }
	}
	universe := func(context.Context) ([]string, error) {
		rig.universeReads++
		return append([]string(nil), rig.universe...), rig.universeErr
	}
	rig.warmer = NewDiagnosisWarmer(service, lookup, universe, progress, func() time.Time { return rig.clock }, 0)
	rig.handler = WithDiagnosis(http.NotFoundHandler(), service, lookup, nil, universe, progress, "pod-a",
		func() time.Time { return rig.clock }, 0, rig.warmer)
	return rig
}

func (rig *diagnosisRig) page(t *testing.T, cursor string, limit int) DiagnosisResponse {
	t.Helper()
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	w := httptest.NewRecorder()
	rig.handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/diagnose?"+q.Encode(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body DiagnosisResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	if page, _ := raw["page"].(map[string]any); page != nil {
		body.Page.RowsWritten = int(page["rows"].(float64))
	}
	return body
}

// Every id of the source's set gets exactly one row with a word from the
// closed list, the withheld ones with whose they are to fix, and the page
// proves its own count: the ids the universe has in its range equal the
// rows it wrote. An id the catalog does not list is UNKNOWN with a reason,
// never counted as detecting.
func TestTheDiagnosisGivesEveryListedStrategyOneRowFromTheExistingWords(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"4109", "4101", "4102", "4103", "4105", "4101"}
	body := rig.page(t, "", 0)
	if body.Universe.Status != "ok" || body.Universe.Count != 5 || body.Universe.Digest == "" {
		t.Fatalf("universe = %+v, want five distinct ids", body.Universe)
	}
	if !body.Page.Holds || body.Page.IDsExpected != 5 || body.Page.RowsWritten != 5 || len(body.Strategies) != 5 || body.NextCursor != "" {
		t.Fatalf("page = %+v rows %d next %q, want one holding page of five", body.Page, len(body.Strategies), body.NextCursor)
	}
	want := map[string]struct {
		verdict     StateWord
		action      ActionWord
		reason      string
		attribution string
	}{
		"4101": {StateDefect, ActionServiceFix, "", ""},
		"4102": {StateNotDetecting, ActionServiceFix, "ALGORITHM_NOT_MIGRATED", "alarmd"},
		"4103": {StateDetecting, ActionNone, "", "strategy"},
		"4105": {DiagnosisUnknown, "", UnknownNotYetPublished, ""},
		"4109": {StateNotDetecting, ActionCacheWriterFill, "EFFECTIVE_TIME_SNAPSHOT_UNAVAILABLE", "writer"},
	}
	order := []string{}
	sum := 0
	for _, row := range body.Strategies {
		order = append(order, row.StrategyID)
		w := want[row.StrategyID]
		if row.Verdict != w.verdict || row.Action != w.action {
			t.Errorf("%s = %s/%s, want %s/%s", row.StrategyID, row.Verdict, row.Action, w.verdict, w.action)
		}
		if w.reason != "" && row.Reason != w.reason {
			t.Errorf("%s reason = %q, want %q", row.StrategyID, row.Reason, w.reason)
		}
		if w.attribution != "" && (len(row.Dispositions) == 0 || row.Dispositions[0].Attribution != w.attribution) {
			t.Errorf("%s dispositions = %+v, want attribution %s", row.StrategyID, row.Dispositions, w.attribution)
		}
	}
	for _, n := range body.Page.ByVerdict {
		sum += n
	}
	if strings.Join(order, ",") != "4101,4102,4103,4105,4109" || sum != 5 {
		t.Errorf("order %v sum %d, want numeric order and the verdicts summing to the universe", order, sum)
	}
}

// A row whose result waits on filling windows carries when they clear, from
// the deciding row's windows and its object's interval; the same row read as
// a defect does not.
func TestADiagnosisRowWaitingOnItsWindowsSaysWhenTheyClear(t *testing.T) {
	end := now.Truncate(time.Minute)
	filling := func(row *Anomaly) {
		row.Wake = &WakeFacts{Known: true, IntervalSeconds: 60, DueAt: now.Add(time.Minute)}
		row.Since, row.RoundSlot, row.Consecutive = now.Add(-10*time.Minute), now.Add(-time.Minute).Unix(), 3
		row.Coverage = &HistoryCoverage{Levels: 1, Short: 1, Guarded: 1, Windows: []WindowRow{{Key: "4101/a/1", Series: "a", Level: 1,
			Valid: 3, Required: 5, End: end, MissingTotal: 2,
			Holes: []WindowHole{{At: end.Add(-4 * time.Minute)}, {At: end.Add(-3 * time.Minute)}}}}}
		// The shape the words read as a window gaining points: a degraded
		// run held by a reactivation guard whose worst window grew.
		row.Kind, row.ReasonCode, row.Cause, row.CauseReason = "DEGRADED_RUN", "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "PLAN_REACTIVATED"
		row.Coverage.WorstValid, row.Coverage.WorstRequired, row.Coverage.PreviousWorstValid, row.Coverage.PreviousKnown = 3, 5, 2, true
	}
	rig := newDiagnosisRigWith(t, diagnosisFacts(), nil, filling)
	rig.universe = []string{"4101"}
	body := rig.page(t, "", 0)
	if len(body.Strategies) != 1 {
		t.Fatalf("rows %+v", body.Strategies)
	}
	row := body.Strategies[0]
	if row.Verdict != StateResultUntrusted || row.WindowClears == nil || !row.WindowClears.At.Equal(end.Add(2*time.Minute)) || !row.WindowClears.Exact {
		t.Fatalf("row %+v clears %+v, want %s", row, row.WindowClears, end.Add(2*time.Minute))
	}
	rig = newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"4101"}
	if got := rig.page(t, "", 0).Strategies[0]; got.WindowClears != nil {
		t.Errorf("a %s row got %+v", got.Verdict, got.WindowClears)
	}
}

// Paging covers the universe exactly once and reads it, and the fleet's
// snapshots, once for the whole diagnosis: later pages reuse the first
// page's read.
func TestDiagnosisPagesCoverTheUniverseOnceAndReadItOnce(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	for i := 1; i <= 7; i++ {
		rig.universe = append(rig.universe, strconv.Itoa(5000+i))
	}
	seen := map[string]int{}
	cursor, pages, total := "", 0, 0
	for {
		body := rig.page(t, cursor, 3)
		pages++
		if !body.Page.Holds || body.UniverseChanged != nil || body.SnapshotReread {
			t.Fatalf("page %d = %+v changed %v reread %v", pages, body.Page, body.UniverseChanged, body.SnapshotReread)
		}
		for _, row := range body.Strategies {
			seen[row.StrategyID]++
		}
		total += body.Page.RowsWritten
		if body.NextCursor == "" {
			break
		}
		cursor = body.NextCursor
	}
	if pages != 3 || total != 7 || len(seen) != 7 || rig.universeReads != 1 {
		t.Fatalf("pages %d total %d distinct %d reads %d, want 3 pages over 7 ids and one read", pages, total, len(seen), rig.universeReads)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s written %d times", id, n)
		}
	}
}

// With no population there is no diagnosis: the universe is unreadable by
// name, no rows, and the page does not hold -- never an empty list that
// reads as nothing wrong. The published catalog is not put in its place.
func TestAnUnreadableUniverseIsAFailedDiagnosisNotAnEmptyOne(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universeErr = errors.New("SOURCE_INCOMPLETE: active set key absent")
	body := rig.page(t, "", 0)
	if body.Universe.Status != "unreadable" || !strings.Contains(body.Universe.Reason, "SOURCE_INCOMPLETE") {
		t.Fatalf("universe = %+v, want unreadable with the reason", body.Universe)
	}
	if body.Page.Holds || len(body.Strategies) != 0 || body.NextCursor != "" {
		t.Fatalf("page = %+v rows %d next %q, want no rows and not holding", body.Page, len(body.Strategies), body.NextCursor)
	}
}

// A page asked after the first page's read has expired reads again, and
// says so; when the source's set moved in between, the page names both
// digests so the CLI reruns rather than stitching two populations.
func TestALaterPageThatRereadsSaysSoAndNamesAChangedUniverse(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"4101", "4102", "4103", "4109"}
	first := rig.page(t, "", 2)
	rig.clock = rig.clock.Add(DiagnosisCacheTTL + time.Second)
	rig.universe = []string{"4101", "4102", "4109"}
	second := rig.page(t, first.NextCursor, 2)
	if !second.SnapshotReread || second.UniverseChanged == nil || second.UniverseChanged.FromDigest != first.Universe.Digest ||
		second.UniverseChanged.ToDigest == first.Universe.Digest || rig.universeReads != 2 {
		t.Fatalf("second = reread %v changed %+v reads %d", second.SnapshotReread, second.UniverseChanged, rig.universeReads)
	}
	if second.Diagnosis != first.Diagnosis {
		t.Errorf("diagnosis id changed %s -> %s", first.Diagnosis, second.Diagnosis)
	}
}

// Progress is read once per page for the page's objects: a Plan found gets
// its slots, a Plan not found and a failed read are each unknown by name,
// and neither changes a verdict.
func TestDiagnosisProgressFillsSlotsAndNamesWhatItCouldNotRead(t *testing.T) {
	var asked [][]string
	rig := newDiagnosisRig(t, diagnosisFacts(), func(_ context.Context, groups []string) (map[string]ProgressFacts, map[string]bool, error) {
		asked = append(asked, groups)
		return map[string]ProgressFacts{"qg-4101-a": {LastFullSlot: 1790150400, NextSlot: 1790150460}}, nil, nil
	})
	rig.universe = []string{"4101", "4103"}
	body := rig.page(t, "", 0)
	if body.Progress != "read" || len(asked) != 1 || strings.Join(asked[0], ",") != "qg-4101-a,qg-4101-b" {
		t.Fatalf("progress %q asked %v, want one read of the page's two objects", body.Progress, asked)
	}
	row := body.Strategies[0]
	if row.Plans[0].LastFullSlot == nil || *row.Plans[0].NextSlot != 1790150460 || row.Plans[1].LastFullSlot != nil {
		t.Errorf("plans = %+v, want a's slots and b's absent", row.Plans)
	}
	if !hasPart(row, "qg-4101-b progress", ProgressNotFound) || row.Verdict != StateDefect {
		t.Errorf("row = %+v, want b's progress unknown and the verdict unchanged", row)
	}

	oneFailed := newDiagnosisRig(t, diagnosisFacts(), func(context.Context, []string) (map[string]ProgressFacts, map[string]bool, error) {
		return map[string]ProgressFacts{}, map[string]bool{"qg-4101-a": true}, nil
	})
	oneFailed.universe = []string{"4103"}
	if body = oneFailed.page(t, "", 0); !hasPart(body.Strategies[0], "qg-4101-a progress", ProgressReadFailed) {
		t.Errorf("a failed object read = %+v, want PROGRESS_READ_FAILED apart from not found", body.Strategies[0].UnknownParts)
	}

	failing := newDiagnosisRig(t, diagnosisFacts(), func(context.Context, []string) (map[string]ProgressFacts, map[string]bool, error) {
		return nil, nil, errors.New("down")
	})
	failing.universe = []string{"4103"}
	body = failing.page(t, "", 0)
	if body.Progress != "unavailable" || !hasPart(body.Strategies[0], "qg-4101-a progress", ProgressUnreadable) ||
		body.Strategies[0].Verdict != StateDetecting {
		t.Errorf("failed read = %q %+v", body.Progress, body.Strategies[0])
	}
}

// With no catalog to answer from, every row is UNKNOWN by name and the
// universe is still counted, so the equation still holds.
func TestWithoutACatalogEveryRowIsUnknownAndTheUniverseIsStillCounted(t *testing.T) {
	rig := newDiagnosisRig(t, nil, nil)
	rig.universe = []string{"1", "2", "3"}
	body := rig.page(t, "", 0)
	if !body.Page.Holds || body.Page.ByVerdict[DiagnosisUnknown] != 3 || body.Universe.Count != 3 {
		t.Fatalf("page = %+v universe %+v", body.Page, body.Universe)
	}
	for _, row := range body.Strategies {
		if row.Reason != UnknownLookupUnavailable {
			t.Errorf("%s reason %q", row.StrategyID, row.Reason)
		}
	}
}

// A page is cut on bytes before a row, never in one: the rows after the
// cut open the next page, and nothing is lost or repeated.
func TestADiagnosisPageIsCutOnBytesWithoutLosingARow(t *testing.T) {
	universe, _ := NormalizeUniverse([]string{"1", "2", "3", "4", "5"})
	big := strings.Repeat("x", DiagnosisPageBytes/2)
	row := func(id string) DiagnosisRow {
		return DiagnosisRow{StrategyID: id, Verdict: StateDetecting, Plans: []DiagnosisPlan{{StrategyPlanRef: StrategyPlanRef{QueryGroup: big}}}}
	}
	var got []string
	after := ""
	for pages := 0; pages < 10; pages++ {
		page := buildDiagnosisPage(universe, after, 0, row)
		if !page.Holds {
			t.Fatalf("page %+v does not hold", page)
		}
		if page.RowsWritten > 1 && !page.TruncatedByBytes {
			t.Fatalf("page of %d half-budget rows not cut", page.RowsWritten)
		}
		for _, r := range page.Rows {
			got = append(got, r.StrategyID)
		}
		if page.Last == "" {
			break
		}
		after = page.Last
	}
	if strings.Join(got, ",") != "1,2,3,4,5" {
		t.Fatalf("rows = %v", got)
	}
}

// Two diagnoses paging at once each keep their own read: interleaved pages
// neither evict the other nor reread, and a universe read that failed is not
// kept, so the next page of that diagnosis reads again.
func TestConcurrentDiagnosesKeepTheirOwnReadAndAFailedReadIsNotKept(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	for i := 1; i <= 6; i++ {
		rig.universe = append(rig.universe, strconv.Itoa(6000+i))
	}
	a1, b1 := rig.page(t, "", 2), rig.page(t, "", 2)
	a2, b2 := rig.page(t, a1.NextCursor, 2), rig.page(t, b1.NextCursor, 2)
	if a1.Diagnosis == b1.Diagnosis || a2.SnapshotReread || b2.SnapshotReread || rig.universeReads != 2 {
		t.Fatalf("ids %s/%s reread %v/%v reads %d, want two diagnoses read once each", a1.Diagnosis, b1.Diagnosis, a2.SnapshotReread, b2.SnapshotReread, rig.universeReads)
	}

	flaky := newDiagnosisRig(t, diagnosisFacts(), nil)
	flaky.universe = []string{"4101", "4102", "4103"}
	first := flaky.page(t, "", 1)
	flaky.universeErr = errors.New("SOURCE_READ_TIMEOUT")
	flaky.clock = flaky.clock.Add(DiagnosisCacheTTL + time.Second)
	failed := flaky.page(t, first.NextCursor, 1)
	flaky.universeErr = nil
	recovered := flaky.page(t, first.NextCursor, 1)
	if failed.Universe.Status != "unreadable" || recovered.Universe.Status != "ok" || len(recovered.Strategies) != 1 {
		t.Fatalf("failed %+v recovered %+v, want the failure not kept", failed.Universe, recovered.Universe)
	}
}

// A follower whose hop to an existing Leader failed refuses the page: a page
// of LOOKUP_UNAVAILABLE rows whose coverage holds would read as a diagnosis.
func TestAFailedForwardToALeaderIsARefusalNotALocalPage(t *testing.T) {
	forward := func(http.ResponseWriter, *http.Request) (bool, string) { return false, ForwardFailed }
	handler := WithDiagnosis(http.NotFoundHandler(), nil, func(string) StrategyLookupFacts { return StrategyLookupFacts{} }, forward,
		func(context.Context) ([]string, error) { return []string{"1"}, nil }, nil, "pod-a", func() time.Time { return now }, 0, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/diagnose", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "LEADER_UNAVAILABLE") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

// The first read serves every request waiting on it, so a first request
// that went away does not fail it: the read runs on its own bound.
func TestADiagnosisReadIsNotCancelledWithTheRequestThatStartedIt(t *testing.T) {
	cache := &diagnosisCache{entries: map[string]*diagnosisEntry{}}
	started, cancelled := context.WithCancel(context.Background())
	var readErr error
	entry, _ := cache.get(started, "d-1", now, func(ctx context.Context) *diagnosisEntry {
		cancelled()
		readErr = ctx.Err()
		return &diagnosisEntry{universe: []string{"1"}, digest: "x", readAt: now, expires: now.Add(time.Minute)}
	})
	if readErr != nil || entry.readError != "" || len(entry.universe) != 1 {
		t.Fatalf("read ctx err %v entry %+v, want the read unaffected by the request's cancellation", readErr, entry)
	}
}

func hasPart(row DiagnosisRow, what, reason string) bool {
	for _, part := range row.UnknownParts {
		if part.What == what && part.Reason == reason {
			return true
		}
	}
	return false
}

// The universe digest is a contract with the CLI, which recomputes it from
// the ids it received to prove the pages covered the set: pinned here and in
// alarmd-cli's TestUniverseDigestMatchesTheServersAlgorithm.
func TestNormalizeUniverseDigestIsPinned(t *testing.T) {
	ids, digest := NormalizeUniverse([]string{"10", "2", "2", "1", "379"})
	if strings.Join(ids, ",") != "1,2,10,379" || digest != "5fa412068ab113ae" {
		t.Fatalf("ids %v digest %s", ids, digest)
	}
}
