// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"strings"
	"testing"
)

// The deployment's no-data group bound is read from the key an operator
// writes, absent means the default, and a value outside 1..1,000,000 is a
// startup failure naming the key.
func TestTheNoDataGroupBoundIsReadFromItsKeyOrRefusedAtLoad(t *testing.T) {
	load := func(leaf string) (Config, error) {
		return LoadWithRoles(writeConfig(t, platformSettingsConfigContents("", platformSettingsControlBase+leaf)), nil)
	}
	stated, err := load("  no_data:\n    max_groups: 5000\n")
	if err != nil || stated.NoDataMaxGroups() != 5000 {
		t.Fatalf("NoDataMaxGroups() = %d, %v; want 5000 from phase_two.no_data.max_groups", stated.NoDataMaxGroups(), err)
	}
	ceiling, err := load("  no_data:\n    max_groups: 1000000\n")
	if err != nil || ceiling.NoDataMaxGroups() != 1000000 {
		t.Fatalf("NoDataMaxGroups() = %d, %v; want the ceiling, 1000000, accepted", ceiling.NoDataMaxGroups(), err)
	}
	silent, err := load("")
	if err != nil || silent.NoDataMaxGroups() != 0 {
		t.Fatalf("a config stating no bound reads %d, %v; want 0 for the store's default", silent.NoDataMaxGroups(), err)
	}
	for _, value := range []string{"0", "-1", "1000001", "1.5"} {
		if _, err := load("  no_data:\n    max_groups: " + value + "\n"); err == nil || !strings.Contains(err.Error(), "max_groups") {
			t.Fatalf("max_groups %s: error = %v, want a refusal naming the key", value, err)
		}
	}
}
