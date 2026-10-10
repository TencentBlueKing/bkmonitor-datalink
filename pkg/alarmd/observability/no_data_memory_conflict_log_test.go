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
	"strings"
	"testing"
)

// A refused memory write says which comparison refused it and what the two
// sides were.
//
// It did not, and the cost was a day. Every no-data write in the fleet was
// refused, the store knew the comparison that failed and both of its values,
// and the line said the reason was not reported -- because a conflict is a
// comparison the write lost rather than a rejection, so it carries no reason
// code and nothing else was rendered. The reading that was needed to tell a
// rollout apart from a race was the pair of revisions, which nobody could see.
func TestARefusedNoDataMemoryWriteLineNamesTheComparisonItLost(t *testing.T) {
	var output bytes.Buffer
	observer := refusalLogObserver(&output, t)
	observer.Observe(context.Background(), Observation{
		Component: ComponentState, Stage: StageNoDataMemoryWritten, Result: ResultDegraded,
		Trace: TraceFields{StrategyID: "11550", BusinessID: "10"},
		NoDataMemoryWrite: &NoDataMemoryWriteFacts{
			Outcome: "CONFLICT", Stored: false, DerivedFrom: "WHOLE_MEMORY",
			Conflict: &NoDataMemoryConflictFacts{
				Kind: "MISSING", Persisted: "stored-digest", Proposed: "proposed-digest",
				ExpectedRevision: 7, StoredRevision: 0,
			},
		},
	})
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want one; log=%s", len(lines), output.String())
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]any{
		"no_data_memory_outcome":           "CONFLICT",
		"no_data_memory_stored":            false,
		"no_data_memory_conflict":          "MISSING",
		"no_data_memory_derived_from":      "WHOLE_MEMORY",
		"no_data_memory_expected_revision": float64(7),
		"no_data_memory_stored_revision":   float64(0),
		"no_data_memory_persisted_digest":  "stored-digest",
		"no_data_memory_proposed_digest":   "proposed-digest",
		"strategy_id":                      "11550",
	} {
		if line[field] != want {
			t.Fatalf("line[%q] = %#v, want %#v; a refusal nobody can read is a refusal nobody can act "+
				"on: line=%#v", field, line[field], want, line)
		}
	}
}

// The ordinary write carries none of it.
//
// A conflict field on every line would put a zero revision beside every
// successful write, and a reader filtering for the comparison would find the
// whole population.
func TestAnAppliedNoDataMemoryWriteLineCarriesNoComparison(t *testing.T) {
	var output bytes.Buffer
	observer := refusalLogObserver(&output, t)
	observer.Observe(context.Background(), Observation{
		Component: ComponentState, Stage: StageNoDataMemoryWritten, Result: ResultSuccess,
		NoDataMemoryWrite: &NoDataMemoryWriteFacts{Outcome: "APPLIED", Stored: true},
	})
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want one; log=%s", len(lines), output.String())
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"no_data_memory_conflict", "no_data_memory_expected_revision",
		"no_data_memory_stored_revision", "no_data_memory_derived_from",
	} {
		if _, present := line[field]; present {
			t.Fatalf("an applied write carried %q; the comparison belongs to the lines that lost one", field)
		}
	}
}
