// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

var defaultPair = [2]string{"cw_object_model_id", "cw_object_model_inst_id"}

func object(method TargetScopeMethod, pairs [][2]string, values ...string) TargetScopeCondition {
	return TargetScopeCondition{Field: TargetScopeObjectModelInst, Method: method, Keys: keys(values...), IdentityFields: pairs}
}

func dims(pairs ...string) map[string]json.RawMessage {
	dimensions := make(map[string]json.RawMessage, len(pairs)/2)
	for index := 0; index+1 < len(pairs); index += 2 {
		dimensions[pairs[index]] = json.RawMessage(pairs[index+1])
	}
	return dimensions
}

func enrich(dimensions map[string]json.RawMessage) Facts {
	return NewChain([]Fuller{IdentityFuller{}}, nil).Enrich(dimensions)
}

// An object-model record is identified by the dimension pair its strategy
// names, "model|instance", and matched against the target's keys like any
// other identity - including exclusion, and including the numeric and
// textual spellings of the same instance id, which the platform emits
// interchangeably.
func TestAnObjectModelTargetMatchesTheRecordsIdentityPair(t *testing.T) {
	plan := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{
		object(TargetScopeInclude, [][2]string{defaultPair}, "switch|12", "switch|7"),
		object(TargetScopeExclude, [][2]string{defaultPair}, "switch|7"),
	}})}
	for name, testCase := range map[string]struct {
		dimensions map[string]json.RawMessage
		admit      bool
		reason     string
	}{
		"numeric instance inside":  {dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `12`), true, ""},
		"textual instance inside":  {dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `"12"`), true, ""},
		"float instance inside":    {dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `12.0`), true, ""},
		"excluded instance":        {dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `7`), false, "out_of_scope"},
		"another model's instance": {dims("cw_object_model_id", `"router"`, "cw_object_model_inst_id", `12`), false, "object_identity_unmatched"},
		"instance not targeted":    {dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `99`), false, "object_identity_unmatched"},
	} {
		facts := enrich(testCase.dimensions)
		decision := (TargetScopeFilter{}).Admit(plan, &facts)
		if decision.Admit != testCase.admit || decision.Reason != testCase.reason {
			t.Errorf("%s: decision = %+v, want admit=%t reason=%q", name, decision, testCase.admit, testCase.reason)
		}
	}
}

// The pairs are tried in the strategy's order and every one the record can
// answer contributes a candidate, so a query that renamed the identity and
// a record in the platform's default spelling both match.
func TestEveryIdentityPairTheRecordAnswersIsACandidate(t *testing.T) {
	pairs := [][2]string{{"model", "inst"}, defaultPair}
	plan := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{
		object(TargetScopeInclude, pairs, "switch|12"),
	}})}
	renamed := enrich(dims("model", `"switch"`, "inst", `12`))
	if decision := (TargetScopeFilter{}).Admit(plan, &renamed); !decision.Admit {
		t.Fatalf("a record keyed by the renamed pair was refused: %+v", decision)
	}
	byDefault := enrich(dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `12`))
	if decision := (TargetScopeFilter{}).Admit(plan, &byDefault); !decision.Admit {
		t.Fatalf("a record keyed by the default pair was refused: %+v", decision)
	}
	// Half a pair is no identity from that pair; the other pair still counts.
	half := enrich(dims("model", `"switch"`, "cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `12`))
	if got := objectIdentityKeys(&half, pairs); !reflect.DeepEqual(got, []string{"switch|12"}) {
		t.Fatalf("candidates = %v, want the one complete pair", got)
	}
}

// A record with no object identity at all cannot be placed, and Python ends
// the group there - even an exclusion does not pass it. The rejection is
// named for what it is, because it is never a legitimate "outside the
// target": either the writer did not reduce a host-family target, or the
// data is not keyed the way the strategy says.
func TestARecordWithoutAnObjectIdentityIsRefusedByName(t *testing.T) {
	plan := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{
		object(TargetScopeInclude, [][2]string{defaultPair}, "switch|12"),
	}})}
	for name, dimensions := range map[string]map[string]json.RawMessage{
		"host data":      dims("bk_target_ip", `"10.0.0.1"`, "bk_target_cloud_id", `0`),
		"only the model": dims("cw_object_model_id", `"switch"`),
		"empty instance": dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `""`),
		"null instance":  dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `null`),
		"no dimensions":  nil,
		"identity under a pair the strategy does not name": dims("model", `"switch"`, "inst", `12`),
	} {
		facts := enrich(dimensions)
		decision := (TargetScopeFilter{}).Admit(plan, &facts)
		if decision.Admit || decision.Reason != "object_identity_missing" {
			t.Errorf("%s: decision = %+v, want object_identity_missing", name, decision)
		}
	}
	excluding := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{
		object(TargetScopeExclude, [][2]string{defaultPair}, "switch|12"),
	}})}
	facts := enrich(nil)
	if decision := (TargetScopeFilter{}).Admit(excluding, &facts); decision.Admit {
		t.Fatalf("an unplaceable record passed an exclusion it could not be evaluated against: %+v", decision)
	}
}

// The specific reasons are only claimed when they are the whole story. A
// scope whose alternatives fail on different attributes, or on the same
// attribute for different reasons, is reported as plain out_of_scope: a
// counter that named one of them would be wrong about the other.
func TestARejectionOnMixedGroundsIsPlainOutOfScope(t *testing.T) {
	facts := enrich(dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `99`))
	facts.SetTopoNodes([]string{"set|1"})

	mixedFields := PlanContext{TargetScope: scope(
		TargetScopeGroup{Conditions: []TargetScopeCondition{object(TargetScopeInclude, [][2]string{defaultPair}, "switch|12")}},
		TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "set|2")}},
	)}
	if decision := (TargetScopeFilter{}).Admit(mixedFields, &facts); decision.Admit || decision.Reason != "out_of_scope" {
		t.Fatalf("alternatives failing on different fields reported %+v", decision)
	}

	mixedKinds := PlanContext{TargetScope: scope(
		TargetScopeGroup{Conditions: []TargetScopeCondition{object(TargetScopeInclude, [][2]string{defaultPair}, "switch|12")}},
		TargetScopeGroup{Conditions: []TargetScopeCondition{object(TargetScopeInclude, [][2]string{{"model", "inst"}}, "switch|12")}},
	)}
	if decision := (TargetScopeFilter{}).Admit(mixedKinds, &facts); decision.Admit || decision.Reason != "out_of_scope" {
		t.Fatalf("one alternative unmatched and one missing reported %+v", decision)
	}

	// Two alternatives both unmatched on the object identity keep the name.
	uniform := PlanContext{TargetScope: scope(
		TargetScopeGroup{Conditions: []TargetScopeCondition{object(TargetScopeInclude, [][2]string{defaultPair}, "switch|12")}},
		TargetScopeGroup{Conditions: []TargetScopeCondition{object(TargetScopeInclude, [][2]string{defaultPair}, "switch|13")}},
	)}
	if decision := (TargetScopeFilter{}).Admit(uniform, &facts); decision.Reason != "object_identity_unmatched" {
		t.Fatalf("alternatives failing the same way reported %+v", decision)
	}
	// A topology absence keeps today's reason: it is Python's ordinary
	// "unplaceable" and nothing downstream is waiting on a new name for it.
	noTopology := enrich(dims("bk_target_ip", `"10.0.0.1"`))
	topology := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "set|2")}})}
	if decision := (TargetScopeFilter{}).Admit(topology, &noTopology); decision.Reason != "out_of_scope" {
		t.Fatalf("a topology absence reported %+v", decision)
	}
}

// The matcher has no case per field: a condition on a field the table does
// not know fails its group rather than widening the target, and a fact the
// host fuller exposed under the host-attribute prefix is readable the moment
// a row names it - the two halves of "a new target is a table row".
func TestTheMatcherIsGenericOverTheAttributeTable(t *testing.T) {
	facts := enrich(dims("bk_host_id", `42`))
	unknown := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{
		{Field: "HOST_ATTR_OS", Method: TargetScopeExclude, Keys: keys("windows")},
	}})}
	if decision := (TargetScopeFilter{}).Admit(unknown, &facts); decision.Admit {
		t.Fatalf("a condition on a field the table does not know admitted the record: %+v", decision)
	}
	facts.HostAttributes = map[string]string{"bk_os_type": "linux", "bk_state": "运营中[需告警]"}
	if got := facts.Candidates(contract.AttributeHostPrefix + "bk_os_type"); !reflect.DeepEqual(got, []string{"linux"}) {
		t.Fatalf("host attribute candidates = %v", got)
	}
	if got := facts.Candidates(contract.AttributeHostPrefix + "bk_cpu"); got != nil {
		t.Fatalf("an attribute the host does not have produced %v", got)
	}
	if got := facts.Candidates("host.identity"); !reflect.DeepEqual(got, []string{"42"}) {
		t.Fatalf("host identity candidates = %v", got)
	}
}

type reportSink struct {
	reports []IdentityReport
}

func (sink *reportSink) receive(report IdentityReport) { sink.reports = append(sink.reports, report) }

// A missing identity is reported with the two things whoever fixes it needs:
// the pairs the strategy expected and the dimensions the record actually
// had. One line per plan per window; the count carries the rest.
func TestAMissingIdentityIsReportedWithExpectedPairsAndSeenDimensions(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	sink := &reportSink{}
	reporter := NewIdentityReporter(func() time.Time { return clock }, time.Minute, sink.receive)
	filter := TargetScopeFilter{Reporter: reporter}
	plan := PlanContext{TenantID: "system", BusinessID: "2", StrategyID: "77", TargetScope: scope(TargetScopeGroup{
		Conditions: []TargetScopeCondition{object(TargetScopeInclude, [][2]string{{"model", "inst"}, defaultPair}, "switch|12")},
	})}
	facts := enrich(dims("bk_target_ip", `"10.0.0.1"`, "bk_target_cloud_id", `0`, "device_name", `"core"`))

	for round := 0; round < 3; round++ {
		if decision := filter.Admit(plan, &facts); decision.Reason != "object_identity_missing" {
			t.Fatalf("decision = %+v", decision)
		}
	}
	if len(sink.reports) != 1 {
		t.Fatalf("reports = %d, want one per plan per window", len(sink.reports))
	}
	report := sink.reports[0]
	if report.TenantID != "system" || report.BusinessID != "2" || report.StrategyID != "77" {
		t.Fatalf("report names %s/%s/%s", report.TenantID, report.BusinessID, report.StrategyID)
	}
	if report.Reason != "object_identity_missing" || report.Count != 1 {
		t.Fatalf("report = %+v, want the first rejection reported at once", report)
	}
	if !reflect.DeepEqual(report.ExpectedPairs, [][2]string{{"model", "inst"}, defaultPair}) {
		t.Fatalf("expected pairs = %v", report.ExpectedPairs)
	}
	if strings.Join(report.DimensionNames, ",") != "bk_target_cloud_id,bk_target_ip,device_name" {
		t.Fatalf("dimension names = %v, want the record's own, sorted", report.DimensionNames)
	}
	// The next window reports again, carrying everything counted since.
	clock = clock.Add(time.Minute)
	filter.Admit(plan, &facts)
	if len(sink.reports) != 2 || sink.reports[1].Count != 3 {
		t.Fatalf("second window reports = %+v, want one line counting the 3 rejections since the first", sink.reports)
	}
	// Another plan is its own line.
	other := plan
	other.StrategyID = "78"
	filter.Admit(other, &facts)
	if len(sink.reports) != 3 || sink.reports[2].StrategyID != "78" {
		t.Fatalf("another plan did not get its own report: %+v", sink.reports)
	}
}

// An unmatched identity is what a record outside the target looks like, so
// it is reported only while the plan admits nothing through its object
// identity - a target that matches something is written in the data's
// representation. The report shows a bounded sample of each side.
func TestAnUnmatchedIdentityIsReportedOnlyWhileThePlanMatchesNothing(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	sink := &reportSink{}
	reporter := NewIdentityReporter(func() time.Time { return clock }, time.Minute, sink.receive)
	filter := TargetScopeFilter{Reporter: reporter}
	plan := PlanContext{StrategyID: "77", TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{
		object(TargetScopeInclude, [][2]string{defaultPair}, "switch|1", "switch|2", "switch|3", "switch|4", "switch|5"),
	}})}
	// A target written in codes against data carrying ids: nothing matches.
	mismatched := enrich(dims("cw_object_model_id", `"sw"`, "cw_object_model_inst_id", `1`))
	if decision := filter.Admit(plan, &mismatched); decision.Reason != "object_identity_unmatched" {
		t.Fatalf("decision = %+v", decision)
	}
	filter.Admit(plan, &mismatched)
	if len(sink.reports) != 1 {
		t.Fatalf("reports = %d, want one", len(sink.reports))
	}
	report := sink.reports[0]
	if report.Reason != "object_identity_unmatched" || report.Count != 1 {
		t.Fatalf("report = %+v", report)
	}
	if !reflect.DeepEqual(report.CandidateKeys, []string{"sw|1"}) {
		t.Fatalf("record keys = %v", report.CandidateKeys)
	}
	if !reflect.DeepEqual(report.TargetKeys, []string{"switch|1", "switch|2", "switch|3"}) {
		t.Fatalf("target sample = %v, want the %d smallest keys", report.TargetKeys, SampleKeys)
	}

	// The plan then matches a record: it is written the way the data is
	// keyed, and its rejections are records outside it. No report.
	clock = clock.Add(time.Minute)
	matched := enrich(dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `2`))
	if decision := filter.Admit(plan, &matched); !decision.Admit {
		t.Fatalf("decision = %+v", decision)
	}
	outside := enrich(dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `9`))
	if decision := filter.Admit(plan, &outside); decision.Reason != "object_identity_unmatched" {
		t.Fatalf("decision = %+v, the counter still names it", decision)
	}
	if len(sink.reports) != 1 {
		t.Fatalf("a plan that admits through its object identity was reported: %+v", sink.reports[1:])
	}
	// A window with no admission and it is reported again, counting every
	// rejection since the previous line.
	clock = clock.Add(time.Minute)
	filter.Admit(plan, &outside)
	if len(sink.reports) != 2 || sink.reports[1].Count != 3 {
		t.Fatalf("reports = %+v, want a second line counting the 3 rejections since the first", sink.reports)
	}
}

// An object identity that hit inside a group another condition then failed
// is not an admission through the object identity: only the group that
// matched as a whole says how the plan matched, and that is what the
// reporter reads to decide whether a plan's unmatched rejections are worth a
// line. The trace is pinned directly because the filter's reason rule
// happens to mask the stale flag today (a plan with an object-free
// alternative never reports unmatched); the invariant is the reporter's,
// not the reason rule's.
func TestAnObjectIdentityHitCountsOnlyForTheGroupThatMatched(t *testing.T) {
	record := enrich(dims("cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `2`, "bk_target_ip", `"10.0.0.1"`))
	record.SetTopoNodes([]string{"module|91"})
	objectThenTopoFails := TargetScopeGroup{Conditions: []TargetScopeCondition{
		object(TargetScopeInclude, [][2]string{defaultPair}, "switch|2"),
		topo(TargetScopeInclude, "module|85"),
	}}
	hostOnly := TargetScopeGroup{Conditions: []TargetScopeCondition{host(TargetScopeInclude, "10.0.0.1|0")}}
	objectThenTopoHolds := TargetScopeGroup{Conditions: []TargetScopeCondition{
		object(TargetScopeInclude, [][2]string{defaultPair}, "switch|2"),
		topo(TargetScopeInclude, "module|91"),
	}}

	var trace matchTrace
	if objectThenTopoFails.matches(&record, &trace) || trace.objectIdentityHit {
		t.Fatalf("a group that failed after its object identity hit left the hit on the trace: %+v", trace)
	}
	if !hostOnly.matches(&record, &trace) || trace.objectIdentityHit {
		t.Fatalf("a host group that matched after an object hit in a failed group reads as an object admission: %+v", trace)
	}
	if !objectThenTopoHolds.matches(&record, &trace) || !trace.objectIdentityHit {
		t.Fatalf("a group that matched through its object identity did not say so: %+v", trace)
	}
	// And the flag follows the latest matched group, not the history.
	if !hostOnly.matches(&record, &trace) || trace.objectIdentityHit {
		t.Fatalf("the hit of an earlier matched group leaked into a later one: %+v", trace)
	}

	// Through the filter: the record admitted through the host group does
	// not silence the unmatched report of a record the object group refuses
	// on its own.
	clock := time.Unix(1700000000, 0)
	sink := &reportSink{}
	filter := TargetScopeFilter{Reporter: NewIdentityReporter(func() time.Time { return clock }, time.Minute, sink.receive)}
	plan := PlanContext{StrategyID: "78", TargetScope: scope(objectThenTopoFails, hostOnly)}
	if decision := filter.Admit(plan, &record); !decision.Admit {
		t.Fatalf("decision = %+v, want admitted through the host group", decision)
	}
	objectOnly := PlanContext{StrategyID: "78", TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{
		object(TargetScopeInclude, [][2]string{defaultPair}, "switch|2"),
	}})}
	mismatched := enrich(dims("cw_object_model_id", `"sw"`, "cw_object_model_inst_id", `1`))
	if decision := filter.Admit(objectOnly, &mismatched); decision.Reason != "object_identity_unmatched" {
		t.Fatalf("decision = %+v", decision)
	}
	if len(sink.reports) != 1 {
		t.Fatalf("reports = %+v, want the unmatched line: the admission above went through the host, not the object identity", sink.reports)
	}
}

// ResolvesToNoHost is answered from host facts, which carry no dimensions,
// so a scope with an object-model condition would read as satisfiable by no
// host and its query would be skipped outright. Such a scope is not decidable
// ahead of the data and the answer is false, whatever the hosts say; a scope
// of host attributes alone is still decided.
func TestAScopeWithAnObjectModelConditionIsNotDecidableFromHosts(t *testing.T) {
	noHosts := func(func(*Facts) bool) {}
	oneHost := func(yield func(*Facts) bool) {
		facts := hostFacts("10.0.0.1|0")
		facts.SetTopoNodes([]string{"module|91"})
		yield(&facts)
	}
	objectScope := scope(TargetScopeGroup{Conditions: []TargetScopeCondition{object(TargetScopeInclude, [][2]string{defaultPair}, "switch|2")}})
	mixedScope := scope(
		TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "module|85")}},
		TargetScopeGroup{Conditions: []TargetScopeCondition{object(TargetScopeInclude, [][2]string{defaultPair}, "switch|2")}},
	)
	for name, candidate := range map[string]*TargetScope{"object only": objectScope, "object beside topology": mixedScope} {
		if candidate.ResolvesToNoHost(noHosts) || candidate.ResolvesToNoHost(oneHost) {
			t.Fatalf("%s: a scope with an object-model condition was decided from hosts", name)
		}
	}
	topoScope := scope(TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "module|85")}})
	if !topoScope.ResolvesToNoHost(oneHost) || !topoScope.ResolvesToNoHost(noHosts) {
		t.Fatalf("a topology scope no host is under was not resolved to no host")
	}
	underScope := scope(TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "module|91")}})
	if underScope.ResolvesToNoHost(oneHost) {
		t.Fatalf("a topology scope a host is under was resolved to no host")
	}
}

// Missing and unmatched do not share a window, a nil reporter counts and
// says nothing, and plans not seen for a long time are forgotten.
func TestTheReporterKeepsItsBookkeepingBounded(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	sink := &reportSink{}
	reporter := NewIdentityReporter(func() time.Time { return clock }, time.Minute, sink.receive)
	plan := PlanContext{StrategyID: "1"}
	reporter.Missing(plan, [][2]string{defaultPair}, nil)
	reporter.Unmatched(plan, []string{"a|1"}, map[string]struct{}{"b|1": {}})
	if len(sink.reports) != 2 {
		t.Fatalf("reports = %d, want the two reasons reported independently", len(sink.reports))
	}
	for strategy := 2; strategy <= 5; strategy++ {
		reporter.Missing(PlanContext{StrategyID: string(rune('0' + strategy))}, nil, nil)
	}
	if len(reporter.plans) != 5 {
		t.Fatalf("tracked plans = %d", len(reporter.plans))
	}
	clock = clock.Add(11 * time.Minute)
	reporter.Missing(PlanContext{StrategyID: "1"}, nil, nil)
	if len(reporter.plans) != 1 {
		t.Fatalf("tracked plans after the sweep = %d, want only the one just seen", len(reporter.plans))
	}

	var none *IdentityReporter
	none.Missing(plan, nil, nil)
	none.Unmatched(plan, nil, nil)
	none.Admitted(plan)
	facts := enrich(nil)
	if decision := (TargetScopeFilter{}).Admit(PlanContext{TargetScope: scope(TargetScopeGroup{
		Conditions: []TargetScopeCondition{object(TargetScopeInclude, [][2]string{defaultPair}, "switch|12")},
	})}, &facts); decision.Reason != "object_identity_missing" {
		t.Fatalf("without a reporter the reason was lost: %+v", decision)
	}
}

// The dimensions handed to enrichment are the series the fingerprint is
// derived from. Enrichment reads them; it does not write them, and the facts
// hold the caller's map rather than a copy that could drift.
func TestEnrichmentLeavesTheDimensionsUntouched(t *testing.T) {
	dimensions := dims(
		"bk_target_ip", `"10.0.0.1"`, "bk_target_cloud_id", `0`, "bk_host_id", `42`,
		"bk_target_service_instance_id", `7`, "cw_object_model_id", `"switch"`, "cw_object_model_inst_id", `12`,
	)
	before := make(map[string]string, len(dimensions))
	for name, raw := range dimensions {
		before[name] = string(raw)
	}
	facts := enrich(dimensions)
	if len(dimensions) != len(before) {
		t.Fatalf("enrichment changed the dimension set: %v", dimensions)
	}
	for name, raw := range dimensions {
		if before[name] != string(raw) {
			t.Fatalf("enrichment rewrote %s: %s -> %s", name, before[name], raw)
		}
	}
	if len(facts.Dimensions) != len(dimensions) {
		t.Fatalf("facts hold %d dimensions, want the caller's %d", len(facts.Dimensions), len(dimensions))
	}
	if len(facts.HostKeys()) != 2 || len(facts.ServiceInstanceKeys()) != 1 {
		t.Fatalf("facts = %+v", facts.Attributes)
	}
}

// Facts learned by the fullers about a record are never a dimension of it.
func TestFactsAreKeptApartFromDimensions(t *testing.T) {
	facts := Facts{Dimensions: dims("a", `1`)}
	facts.AddHostKey("10.0.0.1|0")
	facts.AddHostKey("10.0.0.1|0")
	facts.AddServiceInstanceKey("7")
	facts.SetTopoNodes([]string{"set|2", "module|1", "set|2", ""})
	facts.Set("empty", nil)
	if _, present := facts.Attributes["empty"]; present {
		t.Fatal("an attribute set to nothing was kept")
	}
	if !reflect.DeepEqual(facts.HostKeys(), []string{"10.0.0.1|0"}) || !reflect.DeepEqual(facts.TopoNodes(), []string{"module|1", "set|2"}) {
		t.Fatalf("attributes = %v", facts.Attributes)
	}
	if len(facts.Dimensions) != 1 {
		t.Fatalf("attributes reached the dimensions: %v", facts.Dimensions)
	}
	facts.MarkFactsUnavailable(FactsUnavailableServiceInstanceIndex)
	facts.MarkFactsUnavailable(FactsUnavailableHostIndex)
	if facts.FactsUnavailableReason() != FactsUnavailableServiceInstanceIndex {
		t.Fatalf("the first index to fail did not name the reason: %q", facts.FactsUnavailableReason())
	}
	if (&Facts{HostFactsUnavailable: true}).FactsUnavailableReason() != FactsUnavailableHostIndex {
		t.Fatal("a bare unavailable flag did not read as the host index")
	}
}
