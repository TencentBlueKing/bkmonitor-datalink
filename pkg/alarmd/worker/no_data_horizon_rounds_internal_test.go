// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// noDataPoint is one synthetic series a round produced, as the evaluation will
// read it: the record's dimensions, its value and its period count.
type noDataPoint struct {
	dimensions map[string]json.RawMessage
	value      string
	periods    string
}

func noDataPointsOf(t *testing.T, series []completedSeries) []noDataPoint {
	t.Helper()
	points := make([]noDataPoint, 0, len(series))
	for _, entry := range series {
		if len(entry.inputs) != 1 || len(entry.inputs[0].Inputs) != 1 || entry.inputs[0].Inputs[0].View == nil {
			t.Fatalf("a synthetic series without its one primary binding: %+v", entry.inputs)
		}
		record, ok := entry.inputs[0].Inputs[0].View.Record(0)
		if !ok {
			t.Fatal("a synthetic series carries no record")
		}
		points = append(points, noDataPoint{
			dimensions: record.Dimensions(),
			value:      string(record.Values()[strategy.NoDataValueField]),
			periods:    string(record.Values()[contract.NoDataPeriodFactField]),
		})
	}
	return points
}

// The first round after a gap longer than the horizon says nothing about the
// absence that ran through the gap - no ANOMALY, and above all no NORMAL - and
// the worker stops tracking it (no-data tracking retention proposal, section 1
// item 3 and section 9, "time": an outage that crosses H does not fake a
// recovery).
//
// The worker is down while a group's absence ages, and the first round it
// runs again finds the absence already older than the horizon. Nothing reported
// in the gap, so a NORMAL would be a recovery nobody observed - the consumer
// would close the alert as resolved - and an ANOMALY would be the very absence
// the horizon stops. The round has to produce no series for the group at all.
//
// The bound is crossed with the age fixed on the period grid and the horizon
// moved by a second either way, which is how a deployment meets it: rounds fall
// on the grid and the horizon is any whole number of seconds. The ages around
// three horizons are the shape of the outage the worker is most likely to come
// back from, and nothing different may happen there. The last case is a group
// whose last data came before the gap and which had no absence open: that one
// is reported, as an absence that starts now, counted in periods since the last
// data (nodata-capability-decomposition, section 5.10: LastSeen > 0 gives
// (EvaluationTime - LastSeen) / Period).
func TestTheFirstRoundAfterAGapPastTheHorizonSendsNothingForTheGroup(t *testing.T) {
	wired := noDataWiredPlan(t)
	evaluation := int64(noDataPreflightContract(t, []execution.DuePlan{wired}).Slot.EvaluationTime)
	const groupKey = "bk_target_cloud_id=0,bk_target_ip=192.0.2.30," + contract.NoDataDimensionTag + "=true"
	for _, test := range []struct {
		name     string
		horizon  int64
		memory   execution.NoDataGroupMemory
		reported bool
		periods  string
	}{
		{name: "one second short of the horizon", horizon: 601, reported: true, periods: "11",
			memory: execution.NoDataGroupMemory{LastSeen: evaluation - 660, FirstAbsent: evaluation - 600}},
		{name: "exactly at the horizon", horizon: 600,
			memory: execution.NoDataGroupMemory{LastSeen: evaluation - 660, FirstAbsent: evaluation - 600}},
		{name: "one second past the horizon", horizon: 599,
			memory: execution.NoDataGroupMemory{LastSeen: evaluation - 660, FirstAbsent: evaluation - 600}},
		{name: "one second short of three horizons", horizon: 600,
			memory: execution.NoDataGroupMemory{LastSeen: evaluation - 1859, FirstAbsent: evaluation - 1799}},
		{name: "exactly three horizons", horizon: 600,
			memory: execution.NoDataGroupMemory{LastSeen: evaluation - 1860, FirstAbsent: evaluation - 1800}},
		{name: "one second past three horizons", horizon: 600,
			memory: execution.NoDataGroupMemory{LastSeen: evaluation - 1861, FirstAbsent: evaluation - 1801}},
		{name: "last seen before the gap, no absence open", horizon: 600, reported: true, periods: "30",
			memory: execution.NoDataGroupMemory{LastSeen: evaluation - 1800}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := wired
			plan.CompiledPlan = noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{
				Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"},
				TrackingHorizonSeconds: test.horizon,
			})
			group := test.memory
			group.GroupKey = groupKey
			store := &horizonNoDataStore{groups: []execution.NoDataGroupMemory{group}, present: group.LastSeen}
			stream := noDataWiredStream(t, plan, store)
			if err := stream.loadNoDataMemory(context.Background()); err != nil {
				t.Fatal(err)
			}
			round, err := stream.noDataRoundFor(plan, nil, execution.CompletenessFull)
			if err != nil {
				t.Fatal(err)
			}
			if round.outcome != nodata.OutcomeEvaluated {
				t.Fatalf("outcome = %q, want the round to have judged", round.outcome)
			}
			points := noDataPointsOf(t, round.series)
			for _, point := range points {
				if point.value == strconv.Itoa(nodata.PresentValue) {
					t.Fatalf("the round after the gap produced a NORMAL point for %v: a recovery nobody observed",
						point.dimensions)
				}
			}

			if test.reported {
				if len(points) != 1 || string(points[0].dimensions["bk_target_ip"]) != `"192.0.2.30"` ||
					points[0].value != strconv.Itoa(nodata.AbsentValue) || points[0].periods != test.periods {
					t.Fatalf("points = %+v, want the group's one absent point saying %s periods", points, test.periods)
				}
				if round.facts.Absent != 1 || round.facts.Expired != 0 {
					t.Fatalf("facts = %+v, want the absence reported and nothing expired", round.facts)
				}
				return
			}
			if len(points) != 0 {
				t.Fatalf("points = %+v, want no series at all: the absence ran past the horizon in the gap", points)
			}
			if round.facts.Expired != 1 || round.facts.Absent != 0 {
				t.Fatalf("facts = %+v, want the one absence counted as expired and none reported", round.facts)
			}
			if round.mutation == nil || len(round.mutation.Del) != 1 || round.mutation.Del[0] != groupKey {
				t.Fatalf("mutation = %+v, want the history group forgotten", round.mutation)
			}
			if round.mutation.TrackingExhaustedAt != evaluation {
				t.Fatalf("tracking-exhausted = %d, want this round's %d: the horizon emptied the history roster",
					round.mutation.TrackingExhaustedAt, evaluation)
			}
		})
	}
}

// A synthetic series lives under a runtime state key no real series of the
// item can have, and the closing point of an absence lives under the same key
// as the anomalies it closes.
//
// The key is the series' dimension identity, derived the way the reader
// derives a real series' (access/uq/client.go builds it from the record's
// fields with the same function). The tag is the only thing that separates
// the two, and it separates them as the JSON boolean true: a real series can
// carry a label of that name, but a label is text, and "true" or "True" as text
// is a different identity. Were the tag dropped, or written as text, the
// synthetic series would read and write a real series' trigger state, and an
// absence would be decided on the threshold history of the same host.
func TestTheNoDataSeriesStateKeyIsNeverARealSeriesStateKey(t *testing.T) {
	due := noDataWiredPlan(t)
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	view, err := execution.PlanViewFor(due, execution.SeriesKindNoData)
	if err != nil {
		t.Fatal(err)
	}
	version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	host := map[string]string{"bk_target_ip": "192.0.2.31", "bk_target_cloud_id": "0"}
	group, ok := nodata.Project(host, []string{"bk_target_ip", "bk_target_cloud_id"})
	if !ok {
		t.Fatal("fixture: the host does not project")
	}
	sourceTime := int64(stream.header.Contract.Slot.EvaluationTime) - 60
	keyOf := func(value int) execution.SeriesIdentityDigest {
		t.Helper()
		entry, err := stream.noDataCompletedSeries(view, nodata.SyntheticSeries{
			Group: group, Value: value, SourceTime: sourceTime,
		}, version)
		if err != nil {
			t.Fatal(err)
		}
		if entry.item.Identity.SeriesIdentityDigest != entry.series {
			t.Fatalf("state key %q is not the series %q", entry.item.Identity.SeriesIdentityDigest, entry.series)
		}
		return entry.item.Identity.SeriesIdentityDigest
	}
	absent, present := keyOf(nodata.AbsentValue), keyOf(nodata.PresentValue)
	if absent != present {
		t.Fatalf("the absent point is keyed %s and the closing one %s: the NORMAL that ends an absence "+
			"would be judged on a state that never saw the anomalies", absent, present)
	}

	realKey := func(labels map[string]string) execution.SeriesIdentityDigest {
		t.Helper()
		names := make([]string, 0, len(labels))
		for name := range labels {
			names = append(names, name)
		}
		sort.Strings(names)
		fields := make([]contract.DimensionFieldV2, 0, len(names))
		for _, name := range names {
			encoded, err := json.Marshal(labels[name])
			if err != nil {
				t.Fatal(err)
			}
			fields = append(fields, contract.DimensionFieldV2{Name: name, Value: encoded})
		}
		digest, err := contract.DeriveDimensionIdentityDigestV2(due.Identity.TenantID, due.Identity.BusinessID, fields)
		if err != nil {
			t.Fatal(err)
		}
		return execution.SeriesIdentityDigest(digest)
	}
	for name, labels := range map[string]map[string]string{
		"the host's own series":                    host,
		"a series carrying the tag's name as true": withLabel(host, contract.NoDataDimensionTag, "true"),
		"a series carrying the tag's name as True": withLabel(host, contract.NoDataDimensionTag, "True"),
	} {
		if real := realKey(labels); real == absent {
			t.Fatalf("%s is keyed %s, the same as the no-data series: the two would share one trigger state", name, real)
		}
	}
}

func withLabel(labels map[string]string, name, value string) map[string]string {
	copied := make(map[string]string, len(labels)+1)
	for key, existing := range labels {
		copied[key] = existing
	}
	copied[name] = value
	return copied
}

// A supplement reads an absence the horizon stopped as an absence all the
// same: the stop mark ends the reporting, not the fact that the group had no
// data at the Slot (no-data tracking retention proposal, section 3: a stopped
// group keeps its compact mark beside the timestamps that explain it, and only
// data arriving clears it; section 1 item 3: stopping does not close the alert,
// which may still be standing downstream).
//
// So a late series of a stopped group, for a Slot inside the absence, finds
// the absence recorded and is left alone, exactly as it would be while the
// absence was still tracked. Reading the mark as "nothing recorded" would let
// the supplement decide that Slot as if the group had reported in it. Each case
// is beside its unstopped twin, so the mark is the only difference.
func TestASupplementReadsAnAbsenceTheHorizonStoppedAsRecorded(t *testing.T) {
	due := noDataWiredPlan(t)
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	at := int64(stream.header.Contract.Slot.EvaluationTime)
	host := map[string]string{"bk_target_ip": "192.0.2.32", "bk_target_cloud_id": "0"}
	group, tracked := nodata.Project(host, []string{"bk_target_ip", "bk_target_cloud_id"})
	if !tracked {
		t.Fatal("fixture: the host does not project")
	}
	whole := nodata.WholeItemGroup().Key()
	for _, tc := range []struct {
		name   string
		memory execution.NoDataGroupMemory
		want   noDataStanding
	}{
		{"absent before the Slot, stopped before it", execution.NoDataGroupMemory{
			GroupKey: group.Key(), LastSeen: at - 240, FirstAbsent: at - 180, SuppressedAt: at - 60}, noDataRecorded},
		{"absent before the Slot, stopped at it", execution.NoDataGroupMemory{
			GroupKey: group.Key(), LastSeen: at - 240, FirstAbsent: at - 180, SuppressedAt: at}, noDataRecorded},
		{"absent before the Slot, stopped after it", execution.NoDataGroupMemory{
			GroupKey: group.Key(), LastSeen: at - 240, FirstAbsent: at - 180, SuppressedAt: at + 60}, noDataRecorded},
		{"the whole item absent before the Slot, stopped before it", execution.NoDataGroupMemory{
			GroupKey: whole, FirstAbsent: at - 180, SuppressedAt: at - 60}, noDataRecorded},
		// The mark changes nothing about an absence that began after the
		// Slot: nothing was recorded at the Slot either way.
		{"absent only after the Slot, stopped later", execution.NoDataGroupMemory{
			GroupKey: group.Key(), LastSeen: at, FirstAbsent: at + 60, SuppressedAt: at + 180}, noDataNone},
	} {
		for _, stopped := range []bool{true, false} {
			memory := tc.memory
			if !stopped {
				memory.SuppressedAt = 0
			}
			stream.noData = execution.NoDataLoadResult{Items: []execution.NoDataMemorySnapshot{{
				Identity: due.NoDataIdentity(), Status: execution.NoDataMemoryFound,
				Groups: []execution.NoDataGroupMemory{memory}}}}
			if got := stream.noDataStandingAt(supplementSeries(t, due, at, host)); got != tc.want {
				t.Fatalf("%s (stop mark kept: %t): standing %d, want %d", tc.name, stopped, got, tc.want)
			}
		}
	}
}

// pointsByHost files a round's points by their host address and cloud.
func pointsByHost(t *testing.T, points []noDataPoint) map[string]string {
	t.Helper()
	byHost := make(map[string]string, len(points))
	for _, point := range points {
		key := fmt.Sprintf("%s|%s", point.dimensions["bk_target_ip"], point.dimensions["bk_target_cloud_id"])
		if _, twice := byHost[key]; twice {
			t.Fatalf("two points for %s in one round", key)
		}
		byHost[key] = point.value
	}
	return byHost
}
