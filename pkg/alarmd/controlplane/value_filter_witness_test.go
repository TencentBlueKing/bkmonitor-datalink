// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// withExpression is a source strategy whose item expression is the one given.
func withExpression(t *testing.T, source controlplane.SourceStrategy, expression string) controlplane.SourceStrategy {
	t.Helper()
	written, err := json.Marshal(expression)
	if err != nil {
		t.Fatal(err)
	}
	document := bytes.Replace(source.Document, []byte(`"expression":"a"`), append([]byte(`"expression":`), written...), 1)
	if bytes.Equal(document, source.Document) {
		t.Fatalf("the fixture's expression was not found in %s", source.Document)
	}
	source.Document = document
	return source
}

func requirementsByDataset(t *testing.T, fact execution.FrozenSlotContractFact) map[execution.DatasetName]execution.DataRequirement {
	t.Helper()
	got := make(map[execution.DatasetName]execution.DataRequirement, len(fact.Requirements))
	for _, requirement := range fact.Requirements {
		got[requirement.DatasetName] = requirement
	}
	return got
}

// An OsRestart Plan's primary filters its values ("a <= 3600": hosts that
// restarted within the hour) and its uptime history reads the same source
// unfiltered through its own query: the history witnesses for the primary.
// Both words are derived where the requirement is bound from
// the frozen object, so they hold on a requirement read back from Redis, and
// neither is written into the frozen template, whose bytes are its identity.
func TestAnOsRestartPrimaryIsFilteredAndItsHistoryWitnessesFromTheFrozenObject(t *testing.T) {
	catalog := buildG4Catalog(t, g4SourceStrategy(t, 402, strategy.DetectorKindOsRestart, map[string]any{}))
	fact, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}
	got := requirementsByDataset(t, fact)
	primary, history := got["primary"], got["uptime_history"]
	if primary.Role != execution.InputRolePrimary || !primary.ValueFiltered || primary.UnfilteredWitness {
		t.Fatalf("primary = %+v, want a filtered primary and no witness", primary)
	}
	if history.Role != execution.InputRoleAlgorithmDependency || !history.UnfilteredWitness || history.ValueFiltered {
		t.Fatalf("uptime history = %+v, want an unfiltered witness", history)
	}
	if history.LogicalQueryRef == primary.LogicalQueryRef {
		t.Fatalf("the witness reads the primary's own query %q", history.LogicalQueryRef)
	}
	for _, template := range catalog.QueryGroups[0].Plans[0].RequirementTemplates {
		encoded, err := json.Marshal(template)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(bytes.ToLower(encoded), []byte("witness")) || bytes.Contains(bytes.ToLower(encoded), []byte("filtered")) {
			t.Fatalf("the frozen template carries a derived word: %s", encoded)
		}
	}
}

// Two OsRestart strategies over the same source share one uptime history
// (its identity has no Plan): it witnesses because it does for each of them.
func TestASharedHistoryWitnessesForEveryPlanItServes(t *testing.T) {
	catalog := buildG4Catalog(t,
		g4SourceStrategy(t, 412, strategy.DetectorKindOsRestart, map[string]any{}),
		g4SourceStrategy(t, 413, strategy.DetectorKindOsRestart, map[string]any{}),
	)
	fact, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}
	history := requirementsByDataset(t, fact)["uptime_history"]
	if len(history.Consumers) != 2 || !history.UnfilteredWitness {
		t.Fatalf("shared uptime history = %+v, want two consumers and a witness", history)
	}
}

// A ring ratio's previous point is the primary's own query, filter and all:
// with "a > 0" the primary filters and the previous point witnesses nothing,
// so an empty primary stays on its data line. Without a comparison neither
// word is said.
func TestARingRatiosPreviousPointWitnessesNothing(t *testing.T) {
	for _, test := range []struct {
		expression string
		filtered   bool
	}{
		{expression: "a > 0", filtered: true},
		{expression: "a", filtered: false},
	} {
		t.Run(test.expression, func(t *testing.T) {
			source := g4SourceStrategy(t, 414, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil})
			if test.expression != "a" {
				source = withExpression(t, source, test.expression)
			}
			fact, err := freezeG4Catalog(t, buildG4Catalog(t, source))
			if err != nil {
				t.Fatal(err)
			}
			got := requirementsByDataset(t, fact)
			primary, previous := got["primary"], got["previous"]
			if primary.ValueFiltered != test.filtered {
				t.Fatalf("primary ValueFiltered = %t, want %t", primary.ValueFiltered, test.filtered)
			}
			if previous.LogicalQueryRef != primary.LogicalQueryRef || previous.UnfilteredWitness {
				t.Fatalf("previous = %+v: the primary's own query is no witness", previous)
			}
		})
	}
}

// A threshold Plan's primary is the query group's own, bound without a
// frozen template: it filters when its expression compares, and not
// otherwise.
func TestAThresholdPrimaryFiltersWhenItsExpressionCompares(t *testing.T) {
	for _, test := range []struct {
		expression string
		filtered   bool
	}{
		{expression: "a > 0", filtered: true},
		{expression: "a", filtered: false},
	} {
		t.Run(test.expression, func(t *testing.T) {
			source := controlplane.SourceStrategy{SourceID: "415",
				Document: g4LegacyStrategyDocument(t, 415, strategy.DetectorKindThreshold, "usage", "system.cpu", []string{"host"},
					[]any{map[string]any{"method": "gte", "threshold": 90}}),
				Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}
			if test.expression != "a" {
				source = withExpression(t, source, test.expression)
			}
			fact, err := freezeG4Catalog(t, buildG4Catalog(t, source))
			if err != nil {
				t.Fatal(err)
			}
			if len(fact.Requirements) != 1 || fact.Requirements[0].Role != execution.InputRolePrimary ||
				fact.Requirements[0].ValueFiltered != test.filtered {
				t.Fatalf("requirements %+v, want one primary filtered %t", fact.Requirements, test.filtered)
			}
		})
	}
}
