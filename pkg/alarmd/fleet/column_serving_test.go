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
	"net/http"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Nothing here drove the column parameter at all.
//
// Three columns were added to the view, to the response and to the page, and
// every test kept asking for the default one -- so the whole serving path for
// the other three shipped unexercised, and two defects rode along inside it.
//
// The fixtures put one object in each column with a cause that is plainly not
// this deployment's, which is what makes the column a column: the backend did
// not answer, the window cannot fill, the strategy was edited mid-round.
func columnSnapshots() []Snapshot {
	snapshots := healthySnapshots()

	demoted := anomaly("qg-demoted")
	demoted.Kind = KindDegradedRun
	demoted.CauseReason = "QUERY_UNAVAILABLE"
	demoted.QueryCooldown = &observability.QueryCooldownFacts{
		Event: "entered", Until: now.Add(10 * time.Minute),
		LastQueryAt: now.Add(-time.Hour), Failures: 8,
	}
	snapshots[1].Demoted = []Anomaly{demoted}
	snapshots[1].TotalDemoted = 1

	undecidable := anomaly("qg-undecidable")
	undecidable.Kind = KindDegradedRun
	undecidable.CauseReason = "HISTORY_WARMING"
	snapshots[1].Undecidable = []Anomaly{undecidable}
	snapshots[1].TotalUndecidable = 1

	byDesign := anomaly("qg-by-design")
	byDesign.Kind = KindDegradedRun
	byDesign.CauseReason = "CONFIG_DRIFT"
	snapshots[1].ByDesign = []Anomaly{byDesign}
	snapshots[1].TotalByDesign = 1

	return snapshots
}

// Attribution was filled for the anomaly list and for nothing else, and both
// readers of the field -- the summary counts and the page's own cell -- treated
// an empty value as OURS.
//
// So the demoted pool was served with every row labelled "alarmd 自己该负责的"
// and a summary line saying "判定就看这个数", directly under the paragraph
// explaining that this column is held out of the verdict. A live deployment put
// 58 objects and 18 objects through that, and an operator reading it could not
// decide whether to go to the strategy owner, the data source, or alarmd.
func TestServingAColumnDoesNotReportItsObjectsAsThisDeploymentsFault(t *testing.T) {
	handler := handlerWith(t, columnSnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas())

	for _, column := range []string{ColumnDemoted, ColumnUndecidable, ColumnByDesign} {
		status, body := get(t, handler, "/api/objects?column="+column)
		if status != http.StatusOK {
			t.Fatalf("column %s: status = %d", column, status)
		}
		summary, _ := body["summary"].(map[string]any)
		if summary == nil {
			t.Fatalf("column %s: no summary", column)
		}
		if ours, _ := summary["ours"].(float64); ours != 0 {
			t.Errorf("column %s: summary says %v objects are alarmd's own, want 0 --"+
				" this column exists because they are not", column, ours)
		}
		if external, _ := summary["external"].(float64); external != 1 {
			t.Errorf("column %s: summary says %v external, want 1", column, external)
		}
		rows, _ := body["anomalies"].([]any)
		if len(rows) != 1 {
			t.Fatalf("column %s: %d rows, want 1", column, len(rows))
		}
		row, _ := rows[0].(map[string]any)
		attribution, _ := row["attribution"].(string)
		if attribution == "" {
			t.Errorf("column %s: the row carries no attribution at all; the page reads"+
				" a missing one as alarmd's", column)
		}
		if attribution == string(AttributionOurs) {
			t.Errorf("column %s: the row is attributed %s", column, attribution)
		}
	}
}

// Which list a reader is paging must not change what the deployment's health
// is.
//
// The verdict and the per-replica Ours/External split were computed after the
// column swap, over whichever list had been substituted in -- and the response
// carries both. Asking for the demoted pool therefore answered with a verdict
// decided on the pool, on the same response that the object page reads its
// continuity line from.
func TestTheVerdictIsTheSameWhicheverColumnIsAsked(t *testing.T) {
	snapshots := columnSnapshots()
	// One genuine anomaly, so the verdict has something to be decided by and the
	// test is not comparing two HEALTHYs.
	ours := anomaly("qg-ours")
	ours.Kind = KindDegradedRun
	ours.CauseReason = "REDIS_UNAVAILABLE"
	snapshots[1].Anomalies = []Anomaly{ours}
	snapshots[1].TotalAnomalies = 1
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())

	_, base := get(t, handler, "/api/objects")
	want, _ := base["health"].(string)
	if want != string(HealthDegraded) {
		t.Fatalf("health on the default column = %q, want %s -- the comparison below"+
			" would pass vacuously", want, HealthDegraded)
	}
	for _, column := range []string{ColumnDemoted, ColumnUndecidable, ColumnByDesign} {
		_, body := get(t, handler, "/api/objects?column="+column)
		if got, _ := body["health"].(string); got != want {
			t.Errorf("column %s: health = %q, want %q -- the column being read decided"+
				" the deployment's verdict", column, got, want)
		}
		perReplica, _ := body["per_replica"].([]any)
		total := 0.0
		for _, entry := range perReplica {
			replica, _ := entry.(map[string]any)
			count, _ := replica["ours"].(float64)
			total += count
		}
		if total != 1 {
			t.Errorf("column %s: per-replica ours sums to %v, want 1 -- the split was"+
				" recomputed over the served column", column, total)
		}
	}
}
