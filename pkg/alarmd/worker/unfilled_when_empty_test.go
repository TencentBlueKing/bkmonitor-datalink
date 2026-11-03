// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// An empty answer is a log count's empty for a primary its provider does not
// zero-fill, grouped or not, answered whole and empty with nothing withheld.
// An event count stays quiet, and a time series is neither, grouped or not.
func TestOnlyAnUnfilledCountIsALogCountsEmpty(t *testing.T) {
	grouped := []execution.DataRequirement{{Role: execution.InputRolePrimary, UnfilledCounts: true, RequiredColumns: []string{"host", "value"}}}
	whole := execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty}
	if !(queryAvailabilityEvidence{}).unfilledWhenEmpty(whole, grouped) {
		t.Fatal("a grouped log count answered whole with no group is not labelled")
	}
	ungrouped := []execution.DataRequirement{{Role: execution.InputRolePrimary, UnfilledCounts: true, RequiredColumns: []string{"value"}}}
	if !(queryAvailabilityEvidence{}).unfilledWhenEmpty(whole, ungrouped) {
		t.Fatal("an ungrouped log count answered with no series is not labelled")
	}
	if (queryAvailabilityEvidence{}).quietWhenEmpty(whole, grouped) {
		t.Fatal("a log count read as an event count at rest")
	}
	eventCount := []execution.DataRequirement{{Role: execution.InputRolePrimary, ProviderLeadSeconds: 240, RequiredColumns: []string{"host", "value"}}}
	if (queryAvailabilityEvidence{}).unfilledWhenEmpty(whole, eventCount) || !(queryAvailabilityEvidence{}).quietWhenEmpty(whole, eventCount) {
		t.Fatal("an event count of groups is no longer quiet, or reads as a log count")
	}
	for name, test := range map[string]struct {
		evidence     queryAvailabilityEvidence
		primary      execution.PrimaryInputFact
		requirements []execution.DataRequirement
	}{
		"time series":           {primary: whole, requirements: []execution.DataRequirement{{Role: execution.InputRolePrimary, RequiredColumns: []string{"host", "value"}}}},
		"ungrouped time series": {primary: whole, requirements: []execution.DataRequirement{{Role: execution.InputRolePrimary, RequiredColumns: []string{"value"}}}},
		"ungrouped with zeros":  {primary: execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, requirements: ungrouped},
		"with data":             {primary: execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, requirements: grouped},
		"partial":               {primary: execution.PrimaryInputFact{Completeness: execution.CompletenessPartial, DataState: execution.DataStateEmpty}, requirements: grouped},
		"emptied by target":     {evidence: queryAvailabilityEvidence{primaryEmptiedOutside: true}, primary: whole, requirements: grouped},
		"withheld":              {evidence: queryAvailabilityEvidence{primaryWithheldOtherwise: true}, primary: whole, requirements: grouped},
	} {
		if test.evidence.unfilledWhenEmpty(test.primary, test.requirements) {
			t.Errorf("%s: labelled as a log count's empty", name)
		}
	}
}
