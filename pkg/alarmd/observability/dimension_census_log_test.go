// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func censusObservation(facts *DimensionCensusFacts) Observation {
	return Observation{
		Component: ComponentState, Stage: StageDimensionCensus, Result: ResultSuccess,
		Trace:           TraceFields{StrategyID: "4101", BusinessID: "2", QueryGroupKey: "qg-census"},
		DimensionCensus: facts,
	}
}

// The census line carries the reading and the gate that produced it, each
// under its own key. Asserted against the log rather than the struct: the
// renderer lists its attributes by hand, so a field attached to the
// Observation and never listed reads exactly like a build that does not
// report it - which is how the rebalance shard-gate readings stayed invisible
// through three investigations.
func TestTheDimensionCensusLineRendersTheReadingAndTheGateThatTookIt(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	withheldObserver(t, &output).Observe(context.Background(), censusObservation(&DimensionCensusFacts{
		Source: DimensionCensusSourceRound, Status: DimensionCensusStatusWritten,
		Series: 12000, Dimensions: 3, Values: 4096, OverflowValues: 104, OverflowSeries: 300,
		Bytes: 81920, Limit: 524288, PeakBytes: 700 << 20, ShareBytes: 512 << 20,
	}))

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode census log: %v; log=%s", err, output.String())
	}
	want := map[string]any{
		"census_source": "round",
		"census_status": "WRITTEN",
		"census_series": float64(12000),
		"census_values": float64(4096),
		"census_bytes":  float64(81920),
		"census_limit":  float64(524288),
		// Zeros are not the interesting case here; these two are what says
		// whether the bound was reached, and a census with a tail it could
		// not name is a census a split cannot be planned from.
		"census_dimensions":      float64(3),
		"census_overflow_values": float64(104),
		"census_overflow_series": float64(300),
		// The gate's own two numbers, on the same line as the census they
		// admitted: a census that appears - or stops appearing - is a
		// candidate decision, and the decision cannot be checked afterwards
		// from the census alone.
		"census_peak_bytes":  float64(700 << 20),
		"census_share_bytes": float64(512 << 20),
	}
	for field, value := range want {
		got, present := event[field]
		if !present {
			t.Fatalf("the line has no %q: a reader cannot tell 'none' from 'not reported'; event=%#v", field, event)
		}
		if got != value {
			t.Fatalf("event[%q] = %#v, want %#v; event=%#v", field, got, value, event)
		}
	}
}

// A census whose source or outcome is not one this build knows reaches the
// line and the metric under a name, not under its own text. The label sets
// are pre-created from these vocabularies, so a value arriving at runtime is
// a series nobody declared - which is how a bounded label set stops being
// bounded.
func TestACensusWithNoSourceOrOutcomeIsNamedRatherThanCarriedThrough(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	withheldObserver(t, &output).Observe(context.Background(), censusObservation(&DimensionCensusFacts{
		Series: 1, Dimensions: 1, Values: 1,
	}))

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode census log: %v; log=%s", err, output.String())
	}
	if event["census_source"] != DimensionCensusSourceUnknown {
		t.Fatalf("census_source = %#v, want %q: a census with no source is a writer that forgot to say so, "+
			"and that is worth seeing rather than worth hiding", event["census_source"], DimensionCensusSourceUnknown)
	}
	if event["census_status"] != DimensionCensusStatusUnknown {
		t.Fatalf("census_status = %#v, want %q", event["census_status"], DimensionCensusStatusUnknown)
	}

	loose := &DimensionCensusFacts{Source: "made-up", Status: "MADE_UP"}
	normalized := normalizeDimensionCensusFacts(loose)
	if normalized.Source != DimensionCensusSourceUnknown || normalized.Status != DimensionCensusStatusUnknown {
		t.Fatalf("normalize(%+v) = %+v, want both held to their vocabularies", loose, normalized)
	}
	if loose.Source != "made-up" || loose.Status != "MADE_UP" {
		t.Fatalf("normalize edited the caller's facts: %+v", loose)
	}
}

// Every value the vocabularies declare survives normalize. Without this the
// guard above could hold a legitimate source down to "unknown" and nothing
// would be red: the metric would still have a bounded label set, and every
// roster census would be counted as a bug.
func TestEveryDeclaredCensusSourceAndOutcomeSurvivesNormalize(t *testing.T) {
	t.Parallel()

	for _, source := range DimensionCensusSources() {
		if source == DimensionCensusSourceUnknown {
			continue
		}
		got := normalizeDimensionCensusFacts(&DimensionCensusFacts{
			Source: source, Status: DimensionCensusStatusWritten})
		if got.Source != source {
			t.Fatalf("normalize held the declared source %q down to %q", source, got.Source)
		}
	}
	for _, status := range DimensionCensusStatuses() {
		if status == DimensionCensusStatusUnknown {
			continue
		}
		got := normalizeDimensionCensusFacts(&DimensionCensusFacts{
			Source: DimensionCensusSourceRound, Status: status})
		if got.Status != status {
			t.Fatalf("normalize held the declared outcome %q down to %q", status, got.Status)
		}
	}
}
