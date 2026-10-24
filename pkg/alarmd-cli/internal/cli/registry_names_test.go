// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cli

import (
	"os"
	"regexp"
	"testing"
)

// The families the CLI asks for by name are the ones alarmd registers, as
// its generated reference lists them: a name the registry does not have is
// refused by metrics.get, or reads as absent, on every deployment of the
// same tag. The held recovery counter is the one name asked for to be
// absent, and must stay out of the registry.
func TestTheFamiliesTheCLINamesAreTheRegistrys(t *testing.T) {
	reference, err := os.ReadFile("../../../alarmd/metric/METRICS.md")
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, match := range regexp.MustCompile(`(?m)^## (\S+)$`).FindAllSubmatch(reference, -1) {
		registered[string(match[1])] = true
	}
	if len(registered) == 0 {
		t.Fatal("the reference lists no families")
	}
	names := append([]string{metricSourceRefresh, metricScopeClose, metricLoopTurn, metricRecoveryBeside,
		metricEventsByKind, metricEventsRejected, metricRejectedOverrun}, snapshotKeyMetrics...)
	for _, name := range names {
		if !registered[name] {
			t.Errorf("the CLI names %s, which alarmd does not register", name)
		}
	}
	if registered[metricRecoveryHeld] {
		t.Errorf("%s is registered again; the acceptance check expects it gone", metricRecoveryHeld)
	}
}
