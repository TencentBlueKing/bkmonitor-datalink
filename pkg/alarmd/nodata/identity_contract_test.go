// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The dimensions a synthetic series carries are the dimensions the no-data
// anomaly_id is hashed from, so they must hash the way Python's count_md5
// hashes the dict Python builds. The two literals are not derived from this
// code: the whole-item one is the md5 Python's checker wrote into the last
// checkpoint hash of a production strategy that detects the item as a whole,
// and the host one is count_md5 run on the same dict in Python. A tag that
// reaches the hash as the text "true" hashes to something else entirely -
// count_md5 flattens the boolean True and the text "True" together, and
// nothing else.
func TestSyntheticDimensionsHashLikePythonNoDataIdentity(t *testing.T) {
	host, ok := Project(map[string]string{"bk_target_ip": "10.0.0.1", "bk_target_cloud_id": "0"}, hostNoDataDimensions)
	if !ok {
		t.Fatal("fixture: Project() rejected a complete host series")
	}
	for name, test := range map[string]struct {
		series SyntheticSeries
		want   string
	}{
		"whole item": {series: SyntheticSeries{Group: WholeItemGroup()}, want: "3e06a0b6d0560271cafee9f08a6da2d7"},
		"host group": {series: SyntheticSeries{Group: host}, want: "24251420c035b0e1d0ef96ede93a9a39"},
	} {
		t.Run(name, func(t *testing.T) {
			fields := test.series.IdentityFields()
			got, err := contract.PythonObjectMD5(fields)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("PythonObjectMD5(IdentityFields()) = %s, want %s: the tag reaches the hash as %s",
					got, test.want, fields[contract.NoDataDimensionTag])
			}
		})
	}
}

// The point's own time is the period the Slot decided, one period behind the
// evaluation time: it is the second segment of the anomaly_id, and Python's
// check_timestamp is the previous whole period.
func TestSyntheticSeriesSourceTimeIsThePeriodDecided(t *testing.T) {
	whole := WholeItemGroup().Key()
	series := SyntheticSeriesFor(SyntheticInput{
		EvaluationTime: absenceRound2, PeriodSeconds: absencePeriod,
		Result: AbsenceResult{Verdicts: map[string]Verdict{whole: VerdictAnomaly}},
		Memory: map[string]GroupMemory{whole: {FirstAbsent: absenceRound2}},
		Roster: Roster{Groups: map[string]Group{}},
	})
	if len(series) != 1 || series[0].SourceTime != absenceRound2-absencePeriod {
		t.Fatalf("series = %+v, want one series with SourceTime %d", series, absenceRound2-absencePeriod)
	}
}

// The two period counts only disagree when a round did not happen between the
// last data and the first absence - an unavailable round, which sets neither
// LastSeen nor FirstAbsent. Python then reports the count since the last
// point, and says separately that the data is late; a fixture in which the
// absence began the round after the last data cannot tell which count was
// used, because both give the same number there.
func TestSyntheticSeriesPeriodsCountFromTheLastDataWhenARoundWasUnavailable(t *testing.T) {
	memory := GroupMemory{LastSeen: absenceRound1 - 4*absencePeriod, FirstAbsent: absenceRound1 - 2*absencePeriod}
	if got := absentPeriods(memory, absenceRound1, absencePeriod); got != 4 {
		t.Fatalf("absentPeriods(%+v) = %d, want 4 counted from the last data, not 3 from the first absence", memory, got)
	}
}
