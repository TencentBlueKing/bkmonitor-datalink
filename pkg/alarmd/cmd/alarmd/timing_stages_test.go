// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// startSlotTiming stamps every record it emits success, on purpose: it measures
// how long a call took, and the failures on those paths report themselves on
// their own records. Anything reading those records has to know they carry no
// outcome, and two places away from here do -- the object page renders them in
// a trace, and it was printing that stamp as an outcome.
//
// So the stages it is called with are a closed list, and this checks the call
// sites against it. Without this the list is a copy that has to be remembered:
// a third timing call would emit a stage nothing knows is a timing record, the
// page would print it as an outcome, and no test would fail.
//
// It reads the package's own source because that is where the answer is. A test
// that called startSlotTiming itself would only prove the list contains what
// the test passed.
func TestEveryTimingOnlyStageIsDeclaredAsOne(t *testing.T) {
	declared := map[string]bool{}
	for _, stage := range observability.DurationOnlyStages {
		declared[stage] = true
	}
	if len(declared) == 0 {
		t.Fatal("DurationOnlyStages is empty; the check would pass vacuously")
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	// startSlotTiming(ctx, observer, observability.StageX, ...)
	call := regexp.MustCompile(`startSlotTiming\([^)]*?observability\.(Stage[A-Za-z0-9_]+)`)
	found := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Clean(entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, match := range call.FindAllStringSubmatch(string(source), -1) {
			found[match[1]] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("no startSlotTiming call sites found; the check would pass vacuously, " +
			"and if the calls moved the page's list is now unanchored")
	}

	// The constant name against the value it holds. Comparing names would pass
	// on a constant whose value was changed underneath it.
	byName := map[string]string{
		"StageSlotSourceCompleted": observability.StageSlotSourceCompleted,
		"StageRunnerCompleted":     observability.StageRunnerCompleted,
	}
	for name := range found {
		value, known := byName[name]
		if !known {
			t.Errorf("startSlotTiming is called with observability.%s, which this test does not "+
				"know the value of: add it here and to DurationOnlyStages, or the page will "+
				"print its timing records as outcomes", name)
			continue
		}
		if !declared[value] {
			t.Errorf("startSlotTiming emits stage %q but DurationOnlyStages does not list it: "+
				"the page renders it as an outcome, and its success stamp is not one", value)
		}
	}
	// The other direction: a stage listed here that nothing emits is a page
	// suppressing an outcome that is real.
	for stage := range declared {
		emitted := false
		for name := range found {
			if byName[name] == stage {
				emitted = true
			}
		}
		if !emitted {
			t.Errorf("DurationOnlyStages lists %q but no startSlotTiming call emits it: "+
				"the page is suppressing an outcome that some other emitter reports for real", stage)
		}
	}
}
