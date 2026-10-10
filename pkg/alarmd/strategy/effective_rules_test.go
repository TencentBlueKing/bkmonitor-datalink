package strategy

import (
	"context"
	"encoding/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"os"
	"strings"
	"testing"
	"time"
)

func ruleSecond(value string) int64 {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t.Unix()
}

func TestMaintenanceEffectiveTimeRequiresEveryLevelInactive(t *testing.T) {
	plan := validPlan()
	first := plan.StrategyIR.Levels[0]
	first.TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{"time_ranges": []any{map[string]any{"start": "09:00", "end": "10:00"}}})
	second := first
	second.Definition.LevelID = 2
	second.Definition.Priority = 2
	second.TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{"time_ranges": []any{map[string]any{"start": "11:00", "end": "12:00"}}})
	plan.StrategyIR.Levels = append(plan.StrategyIR.Levels[:0], first, second)
	plan.NoData = &contract.NoDataConfigV1{Continuous: 1, Level: 2}
	plan.EffectiveTimeSnapshot = json.RawMessage(`{"schema_version":1,"status":"READY","business_timezone":"UTC","calendars":[]}`)
	compiled := mustCompilePlan(t, newTestCompiler(t), plan)
	if compiled.NoDataLevel().EffectiveTimeRequirementDigest() != compiled.Levels()[1].EffectiveTimeRequirementDigest() {
		t.Fatal("no-data did not inherit its matching level")
	}
	for _, tt := range []struct {
		at   string
		want string
	}{{"2026-09-22T09:30:00Z", EffectiveTimeActive}, {"2026-09-22T10:30:00Z", EffectiveTimeInactive}, {"2026-09-22T11:30:00Z", EffectiveTimeActive}} {
		fact, err := compiled.ResolveEffectiveTime(context.Background(), ruleSecond(tt.at))
		if err != nil || fact.Status() != tt.want {
			t.Fatalf("%s: %s %v want %s", tt.at, fact.Status(), err, tt.want)
		}
	}
	plan.EffectiveTimeSnapshot = nil
	compiled = mustCompilePlan(t, newTestCompiler(t), plan)
	fact, err := compiled.ResolveEffectiveTime(context.Background(), ruleSecond("2026-09-22T10:30:00Z"))
	if err != nil || fact.Status() != EffectiveTimeUnknown {
		t.Fatalf("unknown levels: %s %v", fact.Status(), err)
	}
	plan.StrategyIR.Levels[0].TriggerPlan.Config = validPlan().StrategyIR.Levels[0].TriggerPlan.Config
	compiled = mustCompilePlan(t, newTestCompiler(t), plan)
	fact, err = compiled.ResolveEffectiveTime(context.Background(), ruleSecond("2026-09-22T10:30:00Z"))
	if err != nil || fact.Status() != EffectiveTimeActive {
		t.Fatalf("ACTIVE plus UNKNOWN cannot close: %s %v", fact.Status(), err)
	}
}
func ruleItem(start, end int64, kind, zone, repeat string) effectiveItem {
	return effectiveItem{ID: 1, Start: &start, End: &end, TimeKind: kind, Timezone: zone, Repeat: json.RawMessage(repeat)}
}
func TestCalendarRulesFixedDates(t *testing.T) {
	tests := []struct {
		name string
		item effectiveItem
		at   string
		want bool
	}{
		{"once-end-inclusive", ruleItem(100, 200, "UNIX_SECONDS", "UTC", `{}`), "1970-01-01T00:03:20Z", true},
		{"once-after-end", ruleItem(100, 200, "UNIX_SECONDS", "UTC", `{}`), "1970-01-01T00:03:21Z", false},
		{"daily-no-repeat", ruleItem(3600, 7200, "DAILY_SECONDS", "Asia/Kolkata", `{}`), "2026-09-22T01:15:00+05:30", true},
		{"daily-cross-midnight", ruleItem(82800, 3600, "DAILY_SECONDS", "UTC", `{}`), "2026-09-22T00:15:00Z", true},
		{"daily-day-ignore-interval", ruleItem(0, 86399, "DAILY_SECONDS", "UTC", `{"freq":"day","interval":3}`), "2026-09-22T12:00:00Z", true},
		{"daily-week-ignore-interval", ruleItem(0, 86399, "DAILY_SECONDS", "UTC", `{"freq":"week","interval":2,"every":[2]}`), "2026-09-22T12:00:00Z", true},
		{"daily-month-ignore-interval", ruleItem(0, 86399, "DAILY_SECONDS", "UTC", `{"freq":"month","interval":3,"every":[22]}`), "2026-09-22T12:00:00Z", true},
		{"daily-year-ignore-interval", ruleItem(0, 86399, "DAILY_SECONDS", "UTC", `{"freq":"year","interval":2,"every":[9]}`), "2026-09-22T12:00:00Z", true},
		{"daily-empty-every", ruleItem(0, 86399, "DAILY_SECONDS", "UTC", `{"freq":"week","interval":2,"every":[]}`), "2026-09-22T12:00:00Z", false},
		{"daily-cross-midnight-current-date", ruleItem(82800, 3600, "DAILY_SECONDS", "UTC", `{"freq":"week","interval":2,"every":[2]}`), "2026-09-22T00:15:00Z", true},
		{"daily-until-evaluation", ruleItem(0, 86399, "DAILY_SECONDS", "UTC", `{"freq":"day","interval":2,"until":100}`), "1970-01-01T00:03:20Z", false},
		{"unix-day-phase-hit", ruleItem(ruleSecond("2000-01-01T10:00:00Z"), ruleSecond("2000-01-01T11:00:00Z"), "UNIX_SECONDS", "UTC", `{"freq":"day","interval":2}`), "2026-09-23T10:30:00Z", true},
		{"unix-day-phase-miss", ruleItem(ruleSecond("2000-01-01T10:00:00Z"), ruleSecond("2000-01-01T11:00:00Z"), "UNIX_SECONDS", "UTC", `{"freq":"day","interval":2}`), "2026-09-22T10:30:00Z", false},
		{"unix-week-multiple-hit", ruleItem(ruleSecond("2026-09-07T10:00:00Z"), ruleSecond("2026-09-07T11:00:00Z"), "UNIX_SECONDS", "UTC", `{"freq":"week","interval":2,"every":[1,3]}`), "2026-09-23T10:30:00Z", true},
		{"unix-week-multiple-miss", ruleItem(ruleSecond("2026-09-07T10:00:00Z"), ruleSecond("2026-09-07T11:00:00Z"), "UNIX_SECONDS", "UTC", `{"freq":"week","interval":2,"every":[1,3]}`), "2026-09-16T10:30:00Z", false},
		{"unix-month-skip-invalid-date", ruleItem(ruleSecond("2024-01-31T10:00:00Z"), ruleSecond("2024-01-31T11:00:00Z"), "UNIX_SECONDS", "UTC", `{"freq":"month","interval":1}`), "2024-03-02T10:30:00Z", false},
		{"unix-month-after-skip", ruleItem(ruleSecond("2024-01-31T10:00:00Z"), ruleSecond("2024-01-31T11:00:00Z"), "UNIX_SECONDS", "UTC", `{"freq":"month","interval":2}`), "2024-03-31T10:30:00Z", true},
		{"unix-year-leap-hit", ruleItem(ruleSecond("2020-02-29T10:00:00Z"), ruleSecond("2020-02-29T11:00:00Z"), "UNIX_SECONDS", "UTC", `{"freq":"year","interval":2}`), "2024-02-29T10:30:00Z", true},
		{"unix-year-nonleap-skipped", ruleItem(ruleSecond("2020-02-29T10:00:00Z"), ruleSecond("2020-02-29T11:00:00Z"), "UNIX_SECONDS", "UTC", `{"freq":"year","interval":1}`), "2023-03-01T10:30:00Z", false},
		{"unix-until-start-not-end", ruleItem(100, 300, "UNIX_SECONDS", "UTC", `{"freq":"day","interval":1,"until":200}`), "1970-01-01T00:04:00Z", true},
		{"unix-dst-gap", ruleItem(ruleSecond("2024-03-09T02:30:00-05:00"), ruleSecond("2024-03-09T03:30:00-05:00"), "UNIX_SECONDS", "America/New_York", `{"freq":"day","interval":1}`), "2024-03-10T03:45:00-04:00", false},
		{"unix-dst-fold-first", ruleItem(ruleSecond("2024-11-02T01:30:00-04:00"), ruleSecond("2024-11-02T02:00:00-04:00"), "UNIX_SECONDS", "America/New_York", `{"freq":"day","interval":1}`), "2024-11-03T01:45:00-04:00", false},
		{"unix-dst-fold-second", ruleItem(ruleSecond("2024-11-02T01:30:00-04:00"), ruleSecond("2024-11-02T02:00:00-04:00"), "UNIX_SECONDS", "America/New_York", `{"freq":"day","interval":1}`), "2024-11-03T01:45:00-05:00", true},
		{"exclude-encoding-differs", ruleItem(0, 86399, "DAILY_SECONDS", "Pacific/Auckland", `{"freq":"day","interval":1,"exclude_date":[1789920000],"exclude_date_encoding_timezone":"Asia/Shanghai"}`), "2026-09-21T12:00:00+12:00", false},
		{"exclude-non-midnight-no-match", ruleItem(0, 86399, "DAILY_SECONDS", "Pacific/Auckland", `{"freq":"day","interval":1,"exclude_date":[1789920001],"exclude_date_encoding_timezone":"Asia/Shanghai"}`), "2026-09-21T12:00:00+12:00", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item, err := compileCalendarItem(tt.item)
			if err != nil {
				t.Fatal(err)
			}
			budget := effectiveRuleCandidateBudget
			got, known := item.matches(ruleSecond(tt.at), &budget)
			if !known || got != tt.want {
				t.Fatalf("matched=%v known=%v want=%v budget=%d", got, known, tt.want, budget)
			}
		})
	}
}

func snapshotForItems(items []effectiveItem) json.RawMessage {
	raw, err := json.Marshal(effectiveSnapshot{SchemaVersion: 1, Status: "READY", BusinessTimezone: "UTC", Calendars: []effectiveCalendar{{ID: 7, TenantID: "tenant-a", Status: "PRESENT", Items: items}}})
	if err != nil {
		panic(err)
	}
	return raw
}
func TestFrozenRulesCalendarFactCoverageAndBudget(t *testing.T) {
	rules, err := compileEffectiveRules(snapshotForItems([]effectiveItem{ruleItem(100, 200, "UNIX_SECONDS", "UTC", `{}`)}), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	facts, err := rules.ResolveCalendarFacts(context.Background(), []CalendarFactRequest{{TenantID: "tenant-a", CalendarID: 7, EvaluationTime: 200}, {TenantID: "tenant-a", CalendarID: 7, EvaluationTime: 201}})
	if err != nil {
		t.Fatal(err)
	}
	if !facts[0].Matched || facts[0].ValidUntil != 201 || facts[1].Matched || !facts[1].Known {
		t.Fatalf("facts=%+v", facts)
	}
	item, err := compileCalendarItem(ruleItem(0, 86400*10000, "UNIX_SECONDS", "UTC", `{"freq":"day","interval":100000}`))
	if err != nil {
		t.Fatal(err)
	}
	budget := 10
	_, known := item.matches(ruleSecond("2026-09-22T00:00:00Z"), &budget)
	if known {
		t.Fatal("truncated long-span search claimed a known answer")
	}
}

func TestSnapshotInvalidNeverFallsBack(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"schema_version":2}`, `{"schema_version":1,"status":"INVALID"}`, `{"schema_version":1,"status":"UNAVAILABLE"}`} {
		plan := validPlan()
		plan.EffectiveTimeSnapshot = json.RawMessage(raw)
		result, err := newTestCompiler(t).Compile(context.Background(), validRequest(plan))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := result.Plan(); ok || result.PlanTerminal() == nil || !strings.HasPrefix(result.PlanTerminal().ReasonCode, "EFFECTIVE_TIME_") {
			t.Fatalf("invalid snapshot accepted: %s", raw)
		}
	}
}

func TestSnapshotFrozenAcrossVersionsAndUptimeStateStable(t *testing.T) {
	plan := validPlan()
	plan.StrategyRef.TenantID = "tenant-a"
	plan.StrategyIR.StrategyRef.TenantID = "tenant-a"
	plan.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{"time_ranges": []any{map[string]any{"start": "00:00", "end": "23:59"}}, "active_calendars": []int{7}})
	plan.EffectiveTimeSnapshot = snapshotForItems([]effectiveItem{ruleItem(100, 200, "UNIX_SECONDS", "UTC", `{}`)})
	compiler := newTestCompiler(t)
	old := mustCompilePlan(t, compiler, plan)
	plan.EffectiveTimeSnapshot = snapshotForItems([]effectiveItem{})
	updated := mustCompilePlan(t, compiler, plan)
	if old.StateCompatibilityHash() != updated.StateCompatibilityHash() {
		t.Fatal("calendar changes reset state")
	}
	for _, check := range []struct {
		p    *CompiledPlan
		want string
	}{{old, EffectiveTimeActive}, {updated, EffectiveTimeInactive}, {old, EffectiveTimeActive}} {
		fact, err := check.p.ResolveEffectiveTime(context.Background(), 150)
		if err != nil {
			t.Fatal(err)
		}
		if fact.Status() != check.want {
			t.Fatalf("status=%s want=%s", fact.Status(), check.want)
		}
	}
}

func TestUptimeSecondsAndEmptyPythonSemantics(t *testing.T) {
	for _, raw := range []string{`{"time_ranges":[],"active_calendars":[7]}`, `null`, `{}`, `{"time_ranges":null}`} {
		requirement, err := CompileUptime(json.RawMessage(raw))
		if err != nil || requirement.Kind() != EffectiveTimeAlways {
			t.Fatalf("%s: %+v %v", raw, requirement, err)
		}
	}
	// Python has no reading of calendars without time_ranges (it raises), so
	// neither does this.
	for _, raw := range []string{`{"calendars":[3]}`, `{"active_calendars":[]}`} {
		if _, err := CompileUptime(json.RawMessage(raw)); err == nil {
			t.Fatalf("%s: accepted an uptime Python cannot read", raw)
		}
	}
	req, err := CompileUptime(json.RawMessage(`{"time_ranges":[{"start":"23:15:59","end":"01:05:20"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !matchesTimeRanges(65, req.timeRanges) || !matchesTimeRanges(23*60+15, req.timeRanges) || matchesTimeRanges(66, req.timeRanges) {
		t.Fatal("minute normalization or crossing midnight wrong")
	}
	for _, clock := range []string{"12:00:60", "12:00:-1", "+1:00", "12:0", "12:00:xx"} {
		if _, ok := parseClockMinute(clock); ok {
			t.Fatalf("accepted %s", clock)
		}
	}
}

func BenchmarkCalendarRuleOldAnchor(b *testing.B) {
	item, err := compileCalendarItem(ruleItem(ruleSecond("1900-01-01T10:00:00Z"), ruleSecond("1900-01-01T11:00:00Z"), "UNIX_SECONDS", "America/New_York", `{"freq":"week","interval":2,"every":[1,3,5]}`))
	if err != nil {
		b.Fatal(err)
	}
	at := ruleSecond("2026-09-22T12:00:00Z")
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		budget := effectiveRuleCandidateBudget
		item.matches(at, &budget)
	}
}

func TestPublisherEffectiveSnapshotFixture(t *testing.T) {
	// The publisher's v1 contract fixture, with formatting only changed.
	raw, err := os.ReadFile("testdata/effective-time-snapshot-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := compileEffectiveRules(raw, "example-tenant")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		second  int64
		matched bool
	}{{1700000000, true}, {1700086400, false}, {1700604800, true}, {1700605100, true}, {1700605101, false}} {
		facts, err := rules.ResolveCalendarFacts(context.Background(), []CalendarFactRequest{{TenantID: "example-tenant", CalendarID: 1, EvaluationTime: tt.second}})
		if err != nil {
			t.Fatal(err)
		}
		if !facts[0].Known || facts[0].Matched != tt.matched {
			t.Fatalf("%d: %+v", tt.second, facts[0])
		}
	}
}

func TestInvalidCalendarInputsAreRejected(t *testing.T) {
	for _, repeat := range []string{`null`, `{"freq":"week","interval":1,"every":[null]}`, `{"freq":"day","interval":1,"exclude_date":[null]}`, `{"freq":"day","interval":1,"exclude_date":[1]}`, `{"freq":"day","interval":0}`, `{"freq":"month","interval":1,"every":[32]}`} {
		if _, err := compileCalendarItem(ruleItem(0, 86400, "UNIX_SECONDS", "UTC", repeat)); err == nil {
			t.Fatalf("accepted %s", repeat)
		}
	}
	for _, mutate := range []func(*effectiveSnapshot){func(s *effectiveSnapshot) { s.Calendars[0].TenantID = "other" }, func(s *effectiveSnapshot) { s.Calendars[0].Status = "DELETED" }, func(s *effectiveSnapshot) { s.Calendars[0].Items = nil }, func(s *effectiveSnapshot) { s.BusinessTimezone = "" }} {
		var snapshot effectiveSnapshot
		if err := json.Unmarshal(snapshotForItems([]effectiveItem{}), &snapshot); err != nil {
			t.Fatal(err)
		}
		mutate(&snapshot)
		raw, _ := json.Marshal(snapshot)
		if _, err := compileEffectiveRules(raw, "tenant-a"); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
}
