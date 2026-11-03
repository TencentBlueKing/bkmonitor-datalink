// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"strings"
	"testing"
)

// An item's own max_groups is a positive whole number up to the ceiling,
// frozen into the Plan; absent leaves the Plan without one, so the
// deployment's bound applies and the Plan is what it was before the field
// existed. Anything else refuses this item by the field's name.
func TestAnItemsOwnNoDataGroupBoundIsFrozenOrRefused(t *testing.T) {
	for name, test := range map[string]struct {
		section string
		want    uint32
		refused bool
	}{
		"absent":           {section: `{"is_enabled":true,"continuous":5}`},
		"null":             {section: `{"is_enabled":true,"continuous":5,"max_groups":null}`},
		"stated":           {section: `{"is_enabled":true,"continuous":5,"max_groups":500}`, want: 500},
		"stated as text":   {section: `{"is_enabled":true,"continuous":5,"max_groups":"500"}`, want: 500},
		"the ceiling":      {section: `{"is_enabled":true,"continuous":5,"max_groups":1000000}`, want: 1000000},
		"zero":             {section: `{"is_enabled":true,"continuous":5,"max_groups":0}`, refused: true},
		"negative":         {section: `{"is_enabled":true,"continuous":5,"max_groups":-1}`, refused: true},
		"a fraction":       {section: `{"is_enabled":true,"continuous":5,"max_groups":1.5}`, refused: true},
		"words":            {section: `{"is_enabled":true,"continuous":5,"max_groups":"many"}`, refused: true},
		"past the ceiling": {section: `{"is_enabled":true,"continuous":5,"max_groups":1000001}`, refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			config, err := frozenNoDataConfig(noDataItem(t, test.section), NoDataPolicy{})
			if test.refused {
				if err == nil || !strings.Contains(err.Error(), "max_groups") {
					t.Fatalf("frozenNoDataConfig() = %+v, %v; want a refusal naming max_groups", config, err)
				}
				return
			}
			if err != nil || config == nil || config.MaxGroups != test.want {
				t.Fatalf("frozenNoDataConfig() = %+v, %v; want max groups %d", config, err, test.want)
			}
		})
	}
}
