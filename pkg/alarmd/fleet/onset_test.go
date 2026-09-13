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
	"fmt"
	"net/http"
	"testing"
	"time"
)

// mixedAgeSnapshots builds the shape a live deployment was read in: a chronic
// backlog older than a day, and a burst that started within the last hour.
func mixedAgeSnapshots(chronic, recent int) []Snapshot {
	snapshots := healthySnapshots()
	for index := 0; index < chronic; index++ {
		item := anomaly(fmt.Sprintf("qg-old-%03d", index))
		item.Since = now.Add(-41*time.Hour + time.Duration(index)*time.Minute)
		snapshots[1].Anomalies = append(snapshots[1].Anomalies, item)
	}
	for index := 0; index < recent; index++ {
		item := anomaly(fmt.Sprintf("qg-new-%03d", index))
		item.Since = now.Add(-time.Duration(index+1) * time.Minute)
		snapshots[1].Anomalies = append(snapshots[1].Anomalies, item)
	}
	snapshots[1].TotalAnomalies = chronic + recent
	return snapshots
}

// A deployment was read with 45 of its 83 objects less than two hours old, and
// not one of them appeared before page three: the list is ordered oldest-first,
// so whatever has just started sorts past the end. The page said in its own
// wording that the first page was the batch worth reading.
//
// Oldest-first is not wrong -- it is why an object wrong for days cannot be
// pushed out of view by a burst -- but it cannot also answer "is something
// happening right now", and that is the question an operator opens this page
// with during an incident.
func TestWhatJustStartedIsReachableWithoutPagingToTheEnd(t *testing.T) {
	handler := handlerWith(t, mixedAgeSnapshots(60, 20), Expectation{QueryGroups: 949, Known: true}, replicas())

	firstPage := func(target string) []string {
		status, body := get(t, handler, target)
		if status != http.StatusOK {
			t.Fatalf("%s: status = %d", target, status)
		}
		rows, _ := body["anomalies"].([]any)
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.(map[string]any)["query_group"].(string))
		}
		return ids
	}

	// The default ordering, and the state being reported: nothing recent on it.
	oldest := firstPage("/api/objects")
	for _, id := range oldest {
		if len(id) > 6 && id[:6] == "qg-new" {
			t.Fatalf("oldest-first put a recent object on the first page (%s); "+
				"this test no longer describes the ordering it exists for", id)
		}
	}

	newest := firstPage("/api/objects?order=newest")
	recent := 0
	for _, id := range newest {
		if len(id) > 6 && id[:6] == "qg-new" {
			recent++
		}
	}
	if recent != 20 {
		t.Errorf("newest-first put %d of the 20 recent objects on the first page: "+
			"what is happening now is still not reachable without paging", recent)
	}
	if newest[0] != "qg-new-000" {
		t.Errorf("first row = %s, want the most recent object", newest[0])
	}
}

// Page two of one ordering must continue page one of that same ordering. If the
// sort ran after paging, the second page would be the second page of the other
// order with this one's twenty rows shuffled -- objects would appear twice and
// others not at all, and nothing on the page would look wrong.
func TestPagingAndOrderingAgreeAcrossPages(t *testing.T) {
	handler := handlerWith(t, mixedAgeSnapshots(30, 30), Expectation{QueryGroups: 949, Known: true}, replicas())

	seen := map[string]int{}
	var previous time.Time
	for offset := 0; offset < 60; offset += DefaultPageSize {
		_, body := get(t, handler, fmt.Sprintf("/api/objects?order=newest&offset=%d", offset))
		rows, _ := body["anomalies"].([]any)
		for _, row := range rows {
			record := row.(map[string]any)
			seen[record["query_group"].(string)]++
			since, err := time.Parse(time.RFC3339Nano, record["since"].(string))
			if err != nil {
				t.Fatalf("parse since: %v", err)
			}
			if !previous.IsZero() && since.After(previous) {
				t.Errorf("%s at offset %d is newer than the row before it: the ordering "+
					"does not hold across pages", record["query_group"], offset)
			}
			previous = since
		}
	}
	if len(seen) != 60 {
		t.Errorf("distinct objects over the whole list = %d, want 60", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("%s appeared %d times across the pages", id, count)
		}
	}
}

// An unknown order is refused rather than defaulted, for the same reason an
// unknown column is: the two orderings return disjoint first pages of one list,
// so a silent fallback answers a different question than the one asked without
// anything in the response saying so.
func TestAnUnknownOrderIsRefusedRatherThanDefaulted(t *testing.T) {
	handler := handlerWith(t, mixedAgeSnapshots(5, 5), Expectation{QueryGroups: 949, Known: true}, replicas())

	status, body := get(t, handler, "/api/objects?order=recent")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %v)", status, body)
	}
	status, body = get(t, handler, "/api/objects")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if body["order"] != OrderOldest {
		t.Errorf("order = %v, want the response to name the ordering it served", body["order"])
	}
}

// The onset counts are over the whole list, not the page. Counted per page they
// would answer with whatever the reader happens to be looking at, which is the
// same failure as reading the composition off twenty rows -- and it is worst on
// the first page, where the answer would always be "nothing recent".
func TestTheOnsetShapeIsCountedOverTheWholeListNotThePage(t *testing.T) {
	handler := handlerWith(t, mixedAgeSnapshots(60, 20), Expectation{QueryGroups: 949, Known: true}, replicas())

	_, body := get(t, handler, "/api/objects")
	summary, _ := body["summary"].(map[string]any)
	onset, _ := summary["onset"].(map[string]any)
	if onset == nil {
		t.Fatal("the response carries no onset shape, so the page can only read the visible rows")
	}
	if got := onset["last_hour"]; got != float64(20) {
		t.Errorf("last_hour = %v, want 20 -- none of which are on this page", got)
	}
	if got := onset["older"]; got != float64(60) {
		t.Errorf("older = %v, want 60", got)
	}
	if got := onset["last_day"]; got != float64(0) {
		t.Errorf("last_day = %v, want 0", got)
	}

	// The same read filtered: the shape has to follow the filter, or it
	// describes a population the rows beneath it are not from.
	_, body = get(t, handler, "/api/objects?replica="+replicas()[1])
	summary, _ = body["summary"].(map[string]any)
	onset, _ = summary["onset"].(map[string]any)
	if got := onset["last_hour"].(float64) + onset["last_day"].(float64) + onset["older"].(float64); got != 80 {
		t.Errorf("the buckets sum to %v over a filtered list whose total is 80", got)
	}
}

// Every listed object falls in exactly one bucket. A bucket boundary that
// double counts or drops inflates or hides the recent population, and the whole
// point of the line is that a reader trusts it instead of paging.
func TestEveryObjectFallsInExactlyOneOnsetBucket(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	// Deliberately on and around both boundaries.
	offsets := []time.Duration{
		0, time.Second, 59 * time.Minute, time.Hour - time.Nanosecond,
		time.Hour, time.Hour + time.Second,
		24*time.Hour - time.Nanosecond, 24 * time.Hour, 25 * time.Hour,
	}
	anomalies := make([]Anomaly, 0, len(offsets))
	for index, offset := range offsets {
		anomalies = append(anomalies, Anomaly{
			QueryGroup: fmt.Sprintf("qg-%d", index), Since: at.Add(-offset)})
	}
	onset := summarize(anomalies, at).Onset
	if got := onset.LastHour + onset.LastDay + onset.Older; got != len(offsets) {
		t.Errorf("buckets sum to %d over %d objects: %+v", got, len(offsets), onset)
	}
	if onset.LastHour != 4 {
		t.Errorf("last_hour = %d, want 4 (strictly under an hour old)", onset.LastHour)
	}
	if onset.LastDay != 3 {
		t.Errorf("last_day = %d, want 3", onset.LastDay)
	}
	if onset.Older != 2 {
		t.Errorf("older = %d, want 2", onset.Older)
	}
	if !onset.NewestSince.Equal(at) {
		t.Errorf("newest_since = %s, want %s", onset.NewestSince, at)
	}
	if !onset.OldestSince.Equal(at.Add(-25 * time.Hour)) {
		t.Errorf("oldest_since = %s, want %s", onset.OldestSince, at.Add(-25*time.Hour))
	}
}
