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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// rowsAsPublished is the view the verdict scrape read before it read the
// replicas' summaries: every replica's whole snapshot, its rows decided at
// the moment it published them, aggregated.
func rowsAsPublished(expectation Expectation, snapshots []Snapshot, names []string, at time.Time, stallAfter time.Duration) View {
	decided := make([]Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		view := publishedView(snapshot, stallAfter)
		rows := snapshot
		rows.Anomalies, rows.Demoted, rows.Undecidable, rows.ByDesign = view.Anomalies, view.Demoted, view.Undecidable, view.ByDesign
		rows.NoData, rows.NoDataMemory, rows.RetainedShare = view.NoData, view.NoDataMemory, view.RetainedShare
		rows.ReadEarly, rows.LateSeries = view.ReadEarly, view.LateSeries
		decided = append(decided, rows)
	}
	return aggregate(expectation, decided, names, at, freshness, &headFacts{rowsDecided: true})
}

// scrapeCounts is what the verdict scrape exports from rows, before the
// labels are bounded: kinds with their earliest since, stalled, failure
// categories, query cooldowns, the check lines' counts and the losses.
// Zero entries are left out of each map, so two readings compare by what
// they count.
type scrapeCounts struct {
	Kinds        map[string]KindRows
	Stalled      int
	Failures     map[string]int
	Cooldown     int
	Checks       map[Check]int
	Losses       map[Loss]int
	GraceUnknown int
}

// scrapeCountsOfRows counts a view of rows the way the scrape counted them
// when it read the rows: every row of the anomaly column, the cooldowns of
// both columns, the lines as the page prints them and the retained records
// at the moment of the read. Written out here rather than from the parts'
// code, which is what it checks.
func scrapeCountsOfRows(view View, at time.Time) scrapeCounts {
	counts := scrapeCounts{Kinds: map[string]KindRows{}, Failures: map[string]int{}, Checks: map[Check]int{}, Losses: map[Loss]int{}}
	for _, anomaly := range view.Anomalies {
		if anomaly.QueryCooldown != nil {
			counts.Cooldown++
		}
		if anomaly.Stalled {
			counts.Stalled++
		}
		kind, seen := counts.Kinds[anomaly.Kind]
		if !seen || anomaly.Since.Before(kind.Since) {
			kind.Since = anomaly.Since
		}
		kind.Count++
		counts.Kinds[anomaly.Kind] = kind
		if anomaly.Failure != nil && anomaly.Failure.Category != "" {
			counts.Failures[anomaly.Failure.Category]++
		}
	}
	for _, demoted := range view.Demoted {
		if demoted.QueryCooldown != nil {
			counts.Cooldown++
		}
	}
	for _, report := range Report(&view, at).Checks {
		if lines := report.LineCount(); lines > 0 {
			counts.Checks[report.Code] = lines
		}
	}
	lossRecords(&view, at, func(_ string, check, _ Check, _ string, skip SkippedSpan, loss Loss, unknown bool) {
		if check == CheckBookkeepingAbandoned {
			return
		}
		counts.Losses[loss]++
		if unknown && at.Sub(skip.At) <= RecentSkipWindow {
			counts.GraceUnknown++
		}
	})
	return counts
}

// scrapeCountsOfParts is the same counts read from a view of summaries and
// the part they add up to.
func scrapeCountsOfParts(t *testing.T, view View, part ReplicaPart, at time.Time) scrapeCounts {
	t.Helper()
	if part.Metrics == nil {
		t.Fatal("the merged part carries no metric rows")
	}
	rows := part.Metrics
	counts := scrapeCounts{Kinds: map[string]KindRows{}, Failures: map[string]int{}, Checks: map[Check]int{}, Losses: map[Loss]int{},
		Stalled: rows.Stalled, Cooldown: rows.Cooldown, GraceUnknown: rows.GraceUnknown}
	for kind, counted := range rows.Kinds {
		counts.Kinds[kind] = counted
	}
	for category, count := range rows.Failures {
		counts.Failures[category] = count
	}
	for check, lines := range CheckLines(&view, part, at) {
		if lines > 0 {
			counts.Checks[check] = lines
		}
	}
	for loss, count := range rows.Losses {
		if count > 0 {
			counts.Losses[loss] = count
		}
	}
	return counts
}

// The scrape's counts read from the replicas' summaries are the counts it
// read from their rows decided as each published them, field for field,
// when each replica published at the moment of the read -- the moment the
// rows' read judged its check lines and losses at -- over the fixtures the
// health route is checked against.
func TestTheScrapesCountsFromSummariesAreItsCountsFromRows(t *testing.T) {
	const stallAfter = 10 * time.Minute
	at := now
	fixtures := summaryFixtures(at)
	fixtures["stalled rows with failures"] = func() ([]Snapshot, Expectation, []string) {
		snapshots := externalRows(3)
		snapshots[1].Anomalies[0].FailingSince = at.Add(-time.Hour)
		snapshots[1].Anomalies[1].FailingSince = at.Add(-2 * time.Hour)
		snapshots[1].Anomalies[0].Failure = &FailureRef{Stage: "query", Category: "source_backend"}
		snapshots[1].Anomalies[2].Failure = &FailureRef{Stage: "query", Category: "internal"}
		// A failure with no category is left out, not counted under one.
		snapshots[1].Anomalies[1].Failure = &FailureRef{Stage: "query"}
		return snapshots, Expectation{QueryGroups: 949, Known: true}, replicas()
	}
	fixtures["skips on two replicas"] = func() ([]Snapshot, Expectation, []string) {
		// Recent skips judged against no restart anchor on both replicas,
		// and one whose every Slot an earlier attempt had applied: a record
		// of bookkeeping interrupted, not of detection lost.
		snapshots := healthySnapshots()
		for index := range snapshots {
			snapshots[index].StartedAt = time.Time{}
			snapshots[index].OwnedObjects = []string{fmt.Sprintf("qg-skipped-%d", index), fmt.Sprintf("qg-applied-%d", index)}
			snapshots[index].GapSkips = map[string]SkippedSpan{
				fmt.Sprintf("qg-skipped-%d", index): {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-time.Minute), Replica: snapshots[index].Replica},
				fmt.Sprintf("qg-applied-%d", index): {FirstSlot: 1, LastSlot: 2, Slots: 2, At: at.Add(-time.Minute), Replica: snapshots[index].Replica,
					Evidence: &SkipEvidence{SlotsApplied: 2}},
			}
		}
		return snapshots, Expectation{QueryGroups: 949, Known: true}, replicas()
	}
	for name, fixture := range fixtures {
		snapshots, expectation, names := fixture()
		for index := range snapshots {
			if snapshots[index].TakenAt.After(at.Add(-freshness)) {
				snapshots[index].TakenAt = at
			}
		}
		want := scrapeCountsOfRows(rowsAsPublished(expectation, snapshots, names, at, stallAfter), at)
		view, part := summariesRoundTrip(t, snapshots, expectation, names, at, stallAfter)
		got := scrapeCountsOfParts(t, view, part, at)
		sameJSON(t, name, got, want)
		if reached := scrapeFixtureReaches[name]; reached != nil && !reached(want) {
			t.Errorf("%s: the fixture does not reach what it is for: %+v", name, want)
		}
	}
}

// scrapeFixtureReaches is what each fixture's counts have to hold, or the
// equality above is between two empty readings.
var scrapeFixtureReaches = map[string]func(scrapeCounts) bool{
	"every row number": func(counts scrapeCounts) bool {
		return len(counts.Kinds) > 1 && counts.Cooldown > 0 && len(counts.Checks) > 1 && len(counts.Losses) > 1
	},
	"skips on two replicas": func(counts scrapeCounts) bool {
		return counts.GraceUnknown == 2 && counts.Losses[LossOngoing] == 2
	},
	"stalled rows with failures": func(counts scrapeCounts) bool {
		return counts.Stalled == 2 && len(counts.Failures) == 2
	},
	"a row about to stall":                     func(counts scrapeCounts) bool { return counts.Stalled == 0 && len(counts.Kinds) > 0 },
	"a replica missing, one stale, a list cut": func(counts scrapeCounts) bool { return len(counts.Kinds) > 0 },
}

// The parts' check lines and losses are judged at the moment each replica
// published, as its rows are: a skip recorded within the recent window when
// the replica published is a loss in progress, whatever the read's moment.
func TestTheScrapesLossesAreJudgedAtTheReplicasPublish(t *testing.T) {
	const stallAfter = 10 * time.Minute
	snapshots := healthySnapshots()
	published := now
	for index := range snapshots {
		snapshots[index].TakenAt = published
	}
	snapshots[0].OwnedObjects = []string{"qg-skipped"}
	snapshots[0].GapSkips = map[string]SkippedSpan{"qg-skipped": {FirstSlot: 1, LastSlot: 3, Slots: 3,
		At: published.Add(-RecentSkipWindow + 10*time.Second), Replica: snapshots[0].Replica}}
	expectation := Expectation{QueryGroups: 949, Known: true}
	// Read within the summary's freshness, past the skip's window.
	read := published.Add(30 * time.Second)
	_, part := summariesRoundTrip(t, snapshots, expectation, replicas(), read, stallAfter)
	if part.Metrics == nil || part.Metrics.Losses[LossOngoing] != 1 || part.Metrics.Losses[LossHistorical] != 0 {
		t.Fatalf("losses %+v, want the skip in progress as of the publish", part.Metrics)
	}
	whole := Aggregate(expectation, snapshots, replicas(), read, freshness)
	if byLoss, _ := lossCensus(&whole, read); byLoss[LossOngoing] != 0 || byLoss[LossHistorical] != 1 {
		t.Fatalf("read at the scrape's moment the skip is %+v; the fixture has to cross the window between publish and read", byLoss)
	}
}

// While an object is held by two replicas each holder's part counts it,
// and the number of such objects is there to read beside the counts; with
// one holder it is zero, and with an owned list unread it is unknown.
func TestHandedOverObjectsAreCountedOncePerHolderAndNumbered(t *testing.T) {
	for name, both := range map[string]bool{"held by both": true, "held by one": false} {
		snapshots, ids := held(both)
		for index := range snapshots {
			if index != 1 && !both {
				continue
			}
			row := anomaly("qg-handed-over")
			row.Replica = snapshots[index].Replica
			snapshots[index].Anomalies = append(snapshots[index].Anomalies, row)
			snapshots[index].TotalAnomalies = len(snapshots[index].Anomalies)
		}
		view, part := summariesRoundTrip(t, snapshots, Expectation{QueryGroups: len(ids), Known: true, IDs: ids}, replicas(), now, time.Minute)
		objects, known := HandoverObjects(&view)
		holders := 1
		if both {
			holders = 2
		}
		counted := 0
		for _, kind := range part.Metrics.Kinds {
			counted += kind.Count
		}
		if !known || objects != holders-1 || counted != holders {
			t.Errorf("%s: handover (%d, %v), rows counted %d; want %d held twice and the row counted once per holder", name, objects,
				known, counted, holders-1)
		}
	}
	snapshots, ids := held(true)
	summaries := make([]ReplicaSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		summaries = append(summaries, SummaryOf(snapshot, snapshot.OwnedObjects, time.Minute))
	}
	view, _ := AggregateSummaries(Expectation{QueryGroups: len(ids), Known: true, IDs: ids}, DigestOf(ids), summaries, replicas(), now,
		freshness, func([]string) ([][]string, bool) { return nil, false })
	if objects, known := HandoverObjects(&view); known {
		t.Fatalf("handover %d known with the owned lists unread, want unknown", objects)
	}
}

// One part without the rows' counts leaves the merged counts unknown, never
// short by that replica's rows, whichever order the parts come in.
func TestOnePartWithoutCountsLeavesTheMergedCountsUnknown(t *testing.T) {
	counted := ReplicaPart{Metrics: &MetricRows{Stalled: 1}}
	for name, parts := range map[string][]ReplicaPart{
		"first": {{}, counted}, "last": {counted, {}},
	} {
		if merged := MergeReplicaParts(parts...); merged.Metrics != nil {
			t.Errorf("%s: merged counts %+v, want unknown", name, merged.Metrics)
		}
	}
	if merged := MergeReplicaParts(counted, counted); merged.Metrics == nil || merged.Metrics.Stalled != 2 {
		t.Fatalf("merged counts %+v, want both parts' rows", merged.Metrics)
	}
}

// A replica with no rows publishes counts of none, which are counts: read
// back they are not the absent counts of a build before them.
func TestAPartWithNoRowsKeepsItsCountsWrittenAndReadBack(t *testing.T) {
	snapshot := healthySnapshots()[0]
	snapshot.TakenAt = now
	encoded, err := json.Marshal(SummaryOf(snapshot, nil, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var summary ReplicaSummary
	if err := json.Unmarshal(encoded, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Part.Metrics == nil {
		t.Fatalf("a replica with no rows read back without counts: %s", encoded)
	}
}

// countingSummaries publishes summaries and snapshots, and counts the reads
// of each.
type countingSummaries struct {
	mu            sync.Mutex
	snapshots     []Snapshot
	summaries     []ReplicaSummary
	snapshotReads [][]string
	summaryReads  int
}

func (store *countingSummaries) Load(_ context.Context, replicas []string) ([]Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.snapshotReads = append(store.snapshotReads, append([]string(nil), replicas...))
	asked := map[string]bool{}
	for _, replica := range replicas {
		asked[replica] = true
	}
	var read []Snapshot
	for _, snapshot := range store.snapshots {
		if asked[snapshot.Replica] {
			read = append(read, snapshot)
		}
	}
	return read, nil
}

func (store *countingSummaries) LoadSummaries(context.Context, []string) ([]ReplicaSummary, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.summaryReads++
	return store.summaries, nil
}

func (store *countingSummaries) LoadOwned(context.Context, []string) (map[string][]string, error) {
	return map[string][]string{}, nil
}

// publishedSummaries is the summaries the snapshots' replicas publish, read
// back as a reader reads them.
func publishedSummaries(t *testing.T, snapshots []Snapshot, stallAfter time.Duration) []ReplicaSummary {
	t.Helper()
	summaries := make([]ReplicaSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		encoded, err := json.Marshal(SummaryOf(snapshot, snapshot.OwnedObjects, stallAfter))
		if err != nil {
			t.Fatal(err)
		}
		var summary ReplicaSummary
		if err := json.Unmarshal(encoded, &summary); err != nil {
			t.Fatal(err)
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

// A scrape reads the summaries once and no snapshot while every replica
// published one with its counts. A replica whose summary is a build's from
// before the counts is read from its snapshot -- that replica alone -- and
// counts what it would have published, never none.
func TestTheScrapeReadsSnapshotsOnlyForSummariesWithoutCounts(t *testing.T) {
	const stallAfter = 10 * time.Minute
	snapshots := externalRows(2)
	snapshots[1].Anomalies[0].FailingSince = now.Add(-time.Hour)
	snapshots[1].Anomalies[1].QueryCooldown = &observability.QueryCooldownFacts{Until: now.Add(time.Hour)}
	for index := range snapshots {
		snapshots[index].TakenAt = now
	}
	expectation := Expectation{QueryGroups: 949, Known: true}
	current := &countingSummaries{snapshots: snapshots, summaries: publishedSummaries(t, snapshots, stallAfter)}
	service := mustService(t, stubExpectations{expectation: expectation}, stubRegistry{replicas: replicas()}, current)
	_, want := service.Summarized(context.Background(), stallAfter)
	if current.summaryReads != 1 || len(current.snapshotReads) != 0 || want.Metrics == nil || want.Metrics.Stalled != 1 {
		t.Fatalf("summary reads %d, snapshot reads %v, counts %+v; want one summary read, no snapshot and the stalled row counted",
			current.summaryReads, current.snapshotReads, want.Metrics)
	}
	older := publishedSummaries(t, snapshots, stallAfter)
	older[1].Part.Metrics = nil
	before := &countingSummaries{snapshots: snapshots, summaries: older}
	service = mustService(t, stubExpectations{expectation: expectation}, stubRegistry{replicas: replicas()}, before)
	_, got := service.Summarized(context.Background(), stallAfter)
	if len(before.snapshotReads) != 1 || len(before.snapshotReads[0]) != 1 || before.snapshotReads[0][0] != snapshots[1].Replica {
		t.Fatalf("snapshot reads %v, want %s's alone", before.snapshotReads, snapshots[1].Replica)
	}
	sameJSON(t, "counts with one replica read from its snapshot", got.Metrics, want.Metrics)
}

// The scrape reads past the observation memory line: with the line refusing
// everything, pages read the deferred gap and the scrape's view and counts
// are the ones it reads with room, the line never asked on its path --
// neither for the summaries nor for a replica read from its snapshot.
func TestTheVerdictScrapesReadNeverAsksTheMemoryLine(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 0)
	for _, snapshot := range snapshotsWithAnomalies(2) {
		snapshot.TakenAt = now
		encoded, _ := json.Marshal(snapshot)
		client.values[store.snapshotKey(snapshot.Replica)] = string(encoded)
	}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}}, stubRegistry{replicas: replicas()}, store)
	store.AdmitLoads(func(uint64) bool { return true })
	withRoom, withRoomPart := service.Summarized(context.Background(), time.Minute)
	asked := 0
	store.AdmitLoads(func(uint64) bool {
		asked++
		return false
	})
	if page := service.View(context.Background()); !hasGap(page, GapSnapshotsDeferred) || asked != 1 {
		t.Fatalf("page read gaps %+v after %d asks, want the deferred gap from one ask", page.Gaps, asked)
	}
	asked = 0
	scrape, part := service.Summarized(context.Background(), time.Minute)
	if asked != 0 || hasGap(scrape, GapSnapshotsDeferred) || part.Metrics == nil {
		t.Fatalf("the scrape's read asked the line %d times, gaps %+v, counts %+v; want it never asked", asked, scrape.Gaps, part.Metrics)
	}
	sameJSON(t, "the scrape's view with the line refusing", scrape, withRoom)
	sameJSON(t, "the scrape's counts with the line refusing", part.Metrics, withRoomPart.Metrics)
}
