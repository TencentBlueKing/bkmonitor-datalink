// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A cutover's line names the Query Groups it changed, so a reading of the
// Slots an edit reached has their Query Groups to join on: nothing else -
// no metric, no CLI - says which ones they were.

// cutoverLineCatalog is one threshold strategy per business, each its own
// Query Group, the index-th strategy edited by edit and given threshold
// threshold(index).
func cutoverLineCatalog(t *testing.T, businesses int, threshold func(int) int, edit func(int, map[string]any)) controlplane.Catalog {
	t.Helper()
	template := realThresholdDocuments(t)[0]
	planner := &businessPlanner{facts: map[string]execution.QueryPlanFacts{}}
	strategies := make([]controlplane.SourceStrategy, 0, businesses)
	for index := 0; index < businesses; index++ {
		business, source := strconv.Itoa(10+index), strconv.Itoa(5000+index)
		var document map[string]any
		if err := json.Unmarshal(withWireIdentity(t, template, "tenant-a", "bkcc__"+business), &document); err != nil {
			t.Fatal(err)
		}
		document["bk_biz_id"], document["id"] = float64(10+index), float64(5000+index)
		if edit != nil {
			edit(index, document)
		}
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if threshold != nil {
			raw = []byte(strings.Replace(string(raw), `"threshold":80`, `"threshold":`+strconv.Itoa(threshold(index)), 1))
		}
		planner.facts[business] = queryFactsFor(t, business, "bkcc__"+business)
		strategies = append(strategies, controlplane.SourceStrategy{SourceID: source, Document: raw,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: business, SpaceScope: "bkcc__" + business}})
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: strategies, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != businesses {
		t.Fatalf("want a Query Group per business, got %d of %d: %+v", len(catalog.QueryGroups), businesses, catalog.Dispositions)
	}
	return catalog
}

// lineGroupOf is the Query Group the catalog put the index-th business's Plan in.
func lineGroupOf(t *testing.T, catalog controlplane.Catalog, index int) string {
	t.Helper()
	business := strconv.Itoa(10 + index)
	for _, group := range catalog.QueryGroups {
		for _, plan := range group.Plans {
			if plan.Identity.BusinessID == business {
				return string(group.Identity)
			}
		}
	}
	t.Fatalf("no Query Group holds business %s", business)
	return ""
}

// cutoverLines is the fixture's repository writing its lines as production
// does - the scoped limiter at the production window, the hourly sample for
// a success - and a reader of the schedule_cutover ones written since the
// last read.
func cutoverLines(t *testing.T, fixture *cutoverFixture) func() []map[string]any {
	t.Helper()
	limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 65536})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := observability.NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	fixture.repository.ConfigureObserver(observability.NewLoggingObserver(observability.New("alarmd", &output), policy))
	return func() []map[string]any {
		t.Helper()
		var lines []map[string]any
		for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
			var line map[string]any
			if raw == "" {
				continue
			}
			if err := json.Unmarshal([]byte(raw), &line); err != nil {
				t.Fatalf("log line is not JSON: %v: %s", err, raw)
			}
			if line["stage"] == "schedule_cutover" {
				lines = append(lines, line)
			}
		}
		output.Reset()
		return lines
	}
}

func sampleOf(line map[string]any, decision string) []string {
	named, _ := line["cutover_"+decision+"_sample"].([]any)
	sample := make([]string, 0, len(named))
	for _, group := range named {
		text, _ := group.(string)
		sample = append(sample, text)
	}
	sort.Strings(sample)
	return sample
}

func countOf(line map[string]any, field string) int {
	number, _ := line[field].(float64)
	return int(number)
}

func TestACutoverThatRevisesOneQueryGroupAndCutsAnotherNamesBoth(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-line-names")
	read := cutoverLines(t, fixture)
	first := cutoverLineCatalog(t, 3, nil, nil)
	fixture.publish(t, first, 60)
	read()
	// The first strategy renamed moves only its output context; the second's
	// threshold changes what runs; the third is left alone.
	second := cutoverLineCatalog(t, 3, func(index int) int {
		if index == 1 {
			return 90
		}
		return 80
	}, func(index int, document map[string]any) {
		if index == 0 {
			renameStrategy(document)
		}
	})
	fixture.publish(t, second, 120)
	lines := read()
	if len(lines) != 1 {
		t.Fatalf("schedule_cutover lines %v, want the one cutover", lines)
	}
	line := lines[0]
	for field, want := range map[string]int{"cutover_kept": 1, "cutover_revised": 1, "cutover_cut": 1, "cutover_added": 0, "cutover_retired": 0,
		"cutover_blocked": 0, "cutover_revised_truncated": 0, "cutover_cut_truncated": 0} {
		if got := countOf(line, field); got != want {
			t.Errorf("%s = %d, want %d (line %v)", field, got, want, line)
		}
	}
	if got, want := sampleOf(line, "revised"), []string{lineGroupOf(t, first, 0)}; !reflect.DeepEqual(got, want) {
		t.Errorf("revised sample %v, want the renamed strategy's Query Group %v", got, want)
	}
	if got, want := sampleOf(line, "cut"), []string{lineGroupOf(t, first, 1)}; !reflect.DeepEqual(got, want) {
		t.Errorf("cut sample %v, want the edited threshold's Query Group %v", got, want)
	}
	for _, field := range []string{"cutover_added_sample", "cutover_retired_sample", "sampled"} {
		if _, present := line[field]; present {
			t.Errorf("%s on a cutover that has none: %v", field, line)
		}
	}
}

func TestACutoverPastTheSampleCapSaysHowManyItDidNotName(t *testing.T) {
	const businesses = 18
	fixture := newCutoverFixture(t, "alarmd:control:cutover-line-cap")
	read := cutoverLines(t, fixture)
	first := cutoverLineCatalog(t, businesses, nil, nil)
	fixture.publish(t, first, 60)
	read()
	fixture.publish(t, cutoverLineCatalog(t, businesses, nil, func(_ int, document map[string]any) { renameStrategy(document) }), 120)
	lines := read()
	if len(lines) != 1 {
		t.Fatalf("schedule_cutover lines %v, want the one cutover", lines)
	}
	all := map[string]bool{}
	for index := 0; index < businesses; index++ {
		all[lineGroupOf(t, first, index)] = true
	}
	named := sampleOf(lines[0], "revised")
	distinct := map[string]bool{}
	for _, group := range named {
		if !all[group] || distinct[group] {
			t.Fatalf("revised sample %v names %s, not one of the renamed Query Groups once", named, group)
		}
		distinct[group] = true
	}
	if countOf(lines[0], "cutover_revised") != businesses || len(named) != 16 || countOf(lines[0], "cutover_revised_truncated") != businesses-16 {
		t.Fatalf("line %v, want %d revised: 16 named and %d not", lines[0], businesses, businesses-16)
	}
}

func TestTwoCutoversThatChangeQueryGroupsWithinAnHourBothWrite(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-line-each")
	read := cutoverLines(t, fixture)
	first := cutoverLineCatalog(t, 2, nil, nil)
	fixture.publish(t, first, 60)
	read()
	// The first strategy renamed, and a minute on the second's threshold
	// edited as well: two cutovers within the hour, each changing one.
	renameFirst := func(index int, document map[string]any) {
		if index == 0 {
			renameStrategy(document)
		}
	}
	fixture.publish(t, cutoverLineCatalog(t, 2, nil, renameFirst), 120)
	fixture.publish(t, cutoverLineCatalog(t, 2, func(index int) int {
		if index == 1 {
			return 90
		}
		return 80
	}, renameFirst), 180)
	lines := read()
	if len(lines) != 2 {
		t.Fatalf("schedule_cutover lines %v, want each of the two cutovers", lines)
	}
	for index, want := range []struct {
		decision string
		group    string
	}{{"revised", lineGroupOf(t, first, 0)}, {"cut", lineGroupOf(t, first, 1)}} {
		line := lines[index]
		if got := sampleOf(line, want.decision); !reflect.DeepEqual(got, []string{want.group}) || line["sampled"] != nil {
			t.Fatalf("cutover %d line %v, want %s naming %s, written whatever the sample", index+1, line, want.decision, want.group)
		}
	}
}
