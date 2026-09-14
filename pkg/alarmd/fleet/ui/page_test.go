// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ui

import (
	"reflect"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"regexp"
	"strings"
	"testing"
)

// The page is embedded in the binary and nothing renders it in a test, so a
// section whose renderer was defined and never wired appeared in production as
// a blank panel with build, vet and every unit test green. That is what
// happened to the capacity panel: the edit that was supposed to add the call
// did not match, changed nothing, and nothing noticed.
//
// This is deliberately a weak check. It cannot say the page is correct, only
// that nothing in it is dead code -- which is the exact shape of that failure.
func TestNoPageFunctionIsDefinedWithoutACallSite(t *testing.T) {
	body := string(page)
	declaration := regexp.MustCompile(`(?m)^function ([A-Za-z0-9_]+)\(`)
	matches := declaration.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("no page functions found; the check would pass vacuously")
	}
	for _, match := range matches {
		name := match[1]
		// One occurrence is the declaration itself. A renderer nobody calls
		// renders nothing, and a panel that renders nothing looks exactly like
		// a deployment with no data.
		if strings.Count(body, name+"(") < 2 {
			t.Errorf("page function %q is defined but never called: its section would render blank", name)
		}
	}
}

// Every panel the page declares has to be filled by something. An element id
// that no script ever writes is a heading with nothing under it.
//
// This asked whether the id appeared anywhere in the file twice, which is not
// the same question. Any id that is also an ordinary word passed on the word:
// removing every script reference to the object table's container left this
// green, because "rows" occurs in the code around it. That is the one container
// on the page it most needed to cover, and the check exists because a panel
// shipped blank with everything green -- so it was failing at the job it was
// written for, in the same way, for a whole class of ids.
//
// It requires one of the forms the page actually addresses an element by now.
var addressedByScript = regexp.MustCompile(`(?:getElementById|\btext|\bshow|\bfail)\('([A-Za-z0-9_]+)'`)

// stageWording returns the body of the map that turns a stage into words, so a
// question about wording cannot be answered by a different map keyed the same
// way.
func stageWording(t *testing.T, body string) string {
	t.Helper()
	block := regexp.MustCompile(`var STAGE_TEXT = \{([^}]*)\}`).FindStringSubmatch(body)
	if block == nil {
		t.Fatal("the page no longer declares STAGE_TEXT: every trace row renders as a raw stage name")
	}
	return block[1]
}

func TestEveryPanelContainerIsWrittenBySomeScript(t *testing.T) {
	body := string(page)
	written := map[string]bool{}
	for _, match := range addressedByScript.FindAllStringSubmatch(body, -1) {
		written[match[1]] = true
	}
	if len(written) == 0 {
		t.Fatal("the page addresses no elements at all; the check would pass vacuously")
	}
	container := regexp.MustCompile(`id="([A-Za-z0-9_]+)"`)
	matches := container.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("no element ids found; the check would pass vacuously")
	}
	seen := map[string]bool{}
	for _, match := range matches {
		id := match[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		// An input the page only ever reads through a variable it captured once
		// is still addressed by one of the forms above at capture time, so this
		// stays a question about whether any script reaches the element.
		if !written[id] {
			t.Errorf("element %q is declared but no script addresses it: it renders empty, "+
				"which looks exactly like a deployment with no such data", id)
		}
	}
}

// A control the page draws but never wires is the same failure as a renderer
// nobody calls, seen from the other side: the operator clicks it and the page
// does nothing, with every test green. The capacity panel was the renderer
// half of this; this is the control half.
func TestEveryIdentifiedButtonIsWired(t *testing.T) {
	body := string(page)
	button := regexp.MustCompile(`<button[^>]*\bid="([A-Za-z0-9_]+)"`)
	matches := button.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("no identified buttons found; the check would pass vacuously")
	}
	for _, match := range matches {
		id := match[1]
		// The page wires every identified button the same way, so that exact
		// form is what is required. A button wired some other way fails this
		// and gets looked at, which is the right outcome: a check that accepts
		// any shape accepts a button with no handler at all.
		if !strings.Contains(body, `getElementById('`+id+`').addEventListener`) {
			t.Errorf("button %q is drawn but nothing listens to it: clicking it does nothing", id)
		}
	}
}

// Every field the page reads off the capacity object has to be a field the API
// actually sends. A name that does not match reads as undefined, the guard in
// front of its cell is then false, and the cell never appears -- with the
// numbers present in every response the whole time. That is how the memory cell
// was missing: the JSON carries memory_used_bytes and the page asked for
// memory_used.
//
// Nothing else catches it. The renderer is called, the container is written,
// build and vet are clean, and the only symptom is a cell that is not there,
// which looks exactly like a deployment that has no such data.
func TestEveryCapacityFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "cap", reflect.TypeOf(fleet.CapacityView{}))
}

// The same failure on the other response, and it shipped too: the records panel
// promised "保留 0 分钟" because the JSON carries retention_seconds and the page
// asked for retention. A missing field is not an error in JavaScript, so the
// page renders the zero and reads as a deployment whose records expire
// instantly.
//
// This is why the detail response is the one response body in that package that
// is exported.
func TestEveryDetailFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "objectDetail", reflect.TypeOf(fleet.DetailResponse{}))
}

// A kind the page has no wording for does not render as an error. It renders as
// whichever word the fallback reaches for, so a new classification appears as an
// ordinary row of a familiar kind -- and the page was doing exactly that, asking
// "is it BLOCKED_RUN, otherwise it is degraded", which quietly renamed every
// kind that came after those two.
func TestThePageHasWordingForEveryAnomalyKind(t *testing.T) {
	if len(fleet.AnomalyKinds) == 0 {
		t.Fatal("no anomaly kinds declared; the check would pass vacuously")
	}
	for _, kind := range fleet.AnomalyKinds {
		if !strings.Contains(string(page), kind+":") {
			t.Errorf("the page has no wording for anomaly kind %q: it would render as another kind's word", kind)
		}
	}
}

// Every column the route serves has to have wording of its own.
//
// The page does not render blank for one it has no entry for: both lookups fall
// through to the anomaly column's, so the heading and the description of the
// to-do list appear over a list of objects that are explicitly not on it. The
// by-design column shipped that way -- the map was keyed "transitional" from an
// earlier name of the column, and nothing here could see it.
func TestEveryServedColumnHasItsOwnHeadingAndDescription(t *testing.T) {
	body := string(page)
	if len(fleet.ObjectColumns) == 0 {
		t.Fatal("no object columns declared; the check would pass vacuously")
	}
	// Closed at the first "};", not at a newline before one. TITLES ends on the
	// same line as its last entry, so a pattern requiring the newline ran past
	// it and swallowed BASIS as well -- and then a column missing from TITLES
	// was found in BASIS and reported as present. The check covered one map
	// twice and the other not at all, which a mutation on TITLES survived.
	for _, block := range []struct{ name, pattern string }{
		{"TITLES", `var TITLES = \{([\s\S]*?)\};`},
		{"BASIS", `var BASIS = \{([\s\S]*?)\};`},
	} {
		found := regexp.MustCompile(block.pattern).FindStringSubmatch(body)
		if found == nil {
			t.Fatalf("the page no longer declares %s: every column renders another column's wording",
				block.name)
		}
		for _, column := range fleet.ObjectColumns {
			if !strings.Contains(found[1], column+":") {
				t.Errorf("%s has no entry for column %q: the list falls through to the anomaly "+
					"column's wording, which is false about every object in it", block.name, column)
			}
		}
	}
}

// One column, one name.
//
// This column was called three different things in five places: 没归到后端 on
// the verdict panel, 自身异常 in the replica table, on the list button and in
// the verdict rule, and 自身异常对象 as the list heading. A reader has no way to
// know those are one number, and they are -- so the replica table and the
// verdict panel read as two separate problems of the same size.
//
// Worse than the names, the two of them made opposite claims: the verdict cell
// said "不等于就是 alarmd 的问题" and the heading over the same objects said
// "alarmd 自己没有把这些对象跑好". Only the first is true; whose problem it is
// gets decided one level down, inside the column.
//
// Checked at each anchor rather than by counting occurrences, because a count
// stays green while one of the sites still says something else.
func TestTheAnomalyColumnIsCalledOneThingEverywhere(t *testing.T) {
	body := string(page)
	const name = "没跑成待查"
	// Names this column used to go by. They are retired rather than allowed as
	// synonyms: a synonym is what made the two panels unreadable together.
	for _, retired := range []string{"没归到后端", "自身异常"} {
		if strings.Contains(body, retired) {
			t.Errorf("the page still calls the anomaly column %q somewhere; it has to be %q everywhere,"+
				" or two panels showing one number read as two problems", retired, name)
		}
	}
	// Every place a reader meets the column. The anchor is something stable on
	// the same source line as the label.
	for _, anchor := range []struct{ what, marker string }{
		{"verdict panel cell", `id="ownBad"`},
		{"replica table header", `<th>其中 alarmd 的</th>`},
		{"object list button", `id="colOwn"`},
		{"object list heading", `anomalies: '`},
	} {
		found := false
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, anchor.marker) && strings.Contains(line, name) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the %s (%s) does not carry the name %q", anchor.what, anchor.marker, name)
		}
	}
}

// The verdict route was the one response this check could not cover, because it
// answered with a map and a map has no fields to reflect over. That is where it
// went wrong: four columns were added to the view and to the page in one change,
// the map in between was not, and the four new cells rendered "undefined" on a
// live deployment for as long as it took someone to read the JSON by hand.
//
// It answers with a type now, so this is the same check as the other two.
func TestEveryVerdictFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "deployment", reflect.TypeOf(fleet.HealthResponse{}))
}

// The impact line is the only thing on the page that answers "what is affected"
// rather than "how many objects", so a field misspelled there renders a zero
// that reads as "nothing is affected" -- which is the one wrong answer that
// makes a reader close the page.
func TestEveryImpactFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "impact", reflect.TypeOf(fleet.Impact{}))
	assertFieldsExist(t, "columnFacts", reflect.TypeOf(fleet.ColumnImpact{}))
}

// The object list is the response the whole table is built from, and it was the
// one this check could not be pointed at: the page read it into a variable
// named d, which three unrelated responses also used, so every field of those
// would have been reported as missing from this one. The variable is named
// objects now, and this is the check that could not run before -- a misspelled
// field on this response renders an empty cell or a silently wrong default in
// the busiest part of the page.
func TestEveryObjectListFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "objects", reflect.TypeOf(fleet.ListResponse{}))
}

// The third response this check could not be pointed at, and the third for the
// same reason: the per-replica view was read into a variable named r, which
// eight callbacks in this page bind to five unrelated types. It is named
// replica now.
//
// This one carries the uptime the object list states as a ceiling on every
// duration below it. A misspelling there does not render a blank -- the guard
// in front of the sentence goes false and the whole sentence disappears, so
// the page silently stops warning that its durations are bounded, which is the
// state it was in before any of this was added.
func TestEveryReplicaFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "replica", reflect.TypeOf(fleet.ReplicaView{}))
}

// The fourth response, and the first where the variable was never the problem:
// summary was always unambiguous, the check simply was never pointed at it.
//
// Which is its own lesson. The other three needed a rename first, so the work
// of adding them made it obvious they were missing; this one needed nothing,
// and stayed missing longer for exactly that reason. A misspelled count here
// renders a sentence that omits a whole bucket of objects without saying so.
func TestEverySummaryFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "summary", reflect.TypeOf(fleet.Summary{}))
}

// The anomaly row is the busiest object on the page -- every cell in the table
// reads it -- and it had no field check at all, for the same reason the list
// response had none: it was read into a variable named a, which matches too
// much to point a check at. It is named anomaly now.
//
// The gap was not theoretical. A misspelled attribution field renders the "谁的
// 问题" cell as though every object were alarmd's own, which is the opposite of
// what that column exists to say, and nothing would have failed.
func TestEveryAnomalyFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "anomaly", reflect.TypeOf(fleet.Anomaly{}))
}

// The coverage counts are read under their own local name so this check can
// exist at all. Read through a one-letter local they would be unreachable by
// any static check, and a misspelling among them is silent in the worst
// possible way: worst_required reads as undefined, the comparison against it
// is false, and every permanently short window renders as "still filling" --
// the exact verdict the field was added to overturn.
func TestEveryWindowCoverageFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "windowCoverage", reflect.TypeOf(fleet.HistoryCoverage{}))
}

// The page decides "this window will never fill" itself rather than reading a
// server-computed flag, so a reader can check the conclusion against the
// numbers printed beside it. That duplication is deliberate, and it is also
// exactly how two copies of one rule drift apart.
//
// This only checks the page still has the function. Whether it decides the
// same thing fleet.HistoryCoverage.Persistent decides is checked by running
// both, in render_smoke_test.go -- a Go reimplementation of the rule compared
// against the Go original would agree with itself no matter what the page did.
func TestThePageStillDecidesWhetherAWindowCanEverFill(t *testing.T) {
	if !regexp.MustCompile(`function windowNeverFills\(windowCoverage\) \{`).Match(page) {
		t.Fatal("the page no longer declares windowNeverFills: every short window renders the same " +
			"way again, which is the state this field was added to end")
	}
}

// One level further down, and the level a reader trusts instead of paging: the
// onset line says how much of the list started recently. A misspelled bucket
// reads as undefined, the guard in front of it is false, and the line simply
// omits that bucket -- so a population that is half an hour old renders as
// though none of it is.
func TestEveryOnsetFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "onset", reflect.TypeOf(fleet.Onset{}))
}

// Anomaly kinds had this check and start-time provenances did not, although the
// consequence is worse: an unmapped kind renders the wrong familiar word, an
// unmapped provenance renders a raw enum beside a timestamp whose meaning that
// name was the only thing explaining -- and three of the six are not a
// measurement at all.
//
// SinceProcessStart went in without either half being updated. The page did get
// wording, by hand; the closed list did not, and nothing failed.
func TestThePageHasWordingForEveryStartTimeProvenance(t *testing.T) {
	if len(fleet.SinceSources) == 0 {
		t.Fatal("no start-time provenances declared; the check would pass vacuously")
	}
	// The wording map specifically, not the page anywhere. A first pass of this
	// asked whether the page contained "<NAME>: '" and passed on a provenance
	// whose wording had been renamed away, because the same key also appears in
	// the map that puts the bound direction on the number. A check satisfied by
	// a different map is satisfied by the wrong thing.
	block := regexp.MustCompile(`var SINCE_SOURCE = \{([^}]*)\}`).FindStringSubmatch(string(page))
	if block == nil {
		t.Fatal("the page no longer declares SINCE_SOURCE: every provenance renders as a raw enum")
	}
	for _, source := range fleet.SinceSources {
		if !strings.Contains(block[1], string(source)+": '") {
			t.Errorf("SINCE_SOURCE has no wording for start-time provenance %q: it renders the raw "+
				"name beside a timestamp that name was supposed to explain", source)
		}
	}
}

// The page tells a reader that a blank cause means the cause was not kept
// rather than that there is none, and it decides that from the provenance. Its
// list of which provenances mean "rebuilt from a record" is a copy of one in
// fleet, and getting it wrong in either direction states something false: a
// missing entry goes back to reading as "no cause", and a spurious one claims a
// watched object was restored.
func TestThePageAgreesOnWhichProvenancesMeanRestored(t *testing.T) {
	body := string(page)
	block := regexp.MustCompile(`var RESTORED_SOURCES = \{([^}]*)\}`).FindStringSubmatch(body)
	if block == nil {
		t.Fatal("the page no longer declares RESTORED_SOURCES: a restored object's blank cause " +
			"reads as having no cause again")
	}
	listed := map[string]bool{}
	for _, match := range regexp.MustCompile(`([A-Z_]+):\s*true`).FindAllStringSubmatch(block[1], -1) {
		listed[match[1]] = true
	}
	declared := map[string]bool{}
	for _, source := range fleet.RestoredSinceSources {
		declared[string(source)] = true
		if !listed[string(source)] {
			t.Errorf("%q is a restored provenance but the page does not treat it as one: "+
				"its blank cause reads as \"there is no cause\"", source)
		}
	}
	for source := range listed {
		if !declared[source] {
			t.Errorf("the page calls %q a restored provenance and fleet does not: it would tell a "+
				"reader a watched object's cause was lost", source)
		}
	}
}

// The page puts the bound direction on the duration itself, from its own copy
// of which provenances are bounds. Getting it wrong in either direction states
// something false about a number a reader is about to act on: a missing entry
// renders a bound as a measurement, a spurious one renders a measured duration
// as an approximation.
//
// The same list now decides whether the sentence above the table may name a
// moment, so a drift here is no longer confined to one cell.
func TestThePageAgreesOnWhichProvenancesAreBounds(t *testing.T) {
	body := string(page)
	block := regexp.MustCompile(`var SINCE_BOUND = \{([^}]*)\}`).FindStringSubmatch(body)
	if block == nil {
		t.Fatal("the page no longer declares SINCE_BOUND: every bound renders as a measurement")
	}
	listed := map[string]bool{}
	for _, match := range regexp.MustCompile(`([A-Z_]+):\s*'`).FindAllStringSubmatch(block[1], -1) {
		listed[match[1]] = true
	}
	if len(fleet.BoundedSinceSources) == 0 {
		t.Fatal("no bounded provenances declared; the check would pass vacuously")
	}
	declared := map[string]bool{}
	for _, source := range fleet.BoundedSinceSources {
		declared[string(source)] = true
		if !listed[string(source)] {
			t.Errorf("%q gives a bound and the page renders it as a measured duration", source)
		}
	}
	for source := range listed {
		if !declared[source] {
			t.Errorf("the page marks %q as a bound and fleet does not: a measured duration renders "+
				"as an approximation", source)
		}
	}
}

// The page suppresses the result word on records that carry only a duration,
// and it held its own copy of which stages those are. A copy is the arrangement
// that goes stale: a third timing call would emit a stage the page does not
// know about, its unconditional success stamp would print as an outcome, and a
// reader following a failed round would be told the failing step succeeded --
// with nothing failing anywhere.
//
// The producing side is checked against the same list in cmd/alarmd, so the two
// ends cannot drift apart without one of them failing.
func TestThePageKnowsEveryStageThatCarriesOnlyADuration(t *testing.T) {
	if len(observability.DurationOnlyStages) == 0 {
		t.Fatal("no duration-only stages declared; the check would pass vacuously")
	}
	body := string(page)
	block := regexp.MustCompile(`var TIMING_ONLY_STAGES = \{([^}]*)\}`).FindStringSubmatch(body)
	if block == nil {
		t.Fatal("the page no longer declares TIMING_ONLY_STAGES: every timing record's " +
			"success stamp is being printed as an outcome again")
	}
	listed := map[string]bool{}
	for _, match := range regexp.MustCompile(`([a-z0-9_]+):\s*true`).FindAllStringSubmatch(block[1], -1) {
		listed[match[1]] = true
	}
	for _, stage := range observability.DurationOnlyStages {
		if !listed[stage] {
			t.Errorf("stage %q carries only a duration but the page does not list it: "+
				"its unconditional success stamp renders as an outcome", stage)
		}
		// A row whose only content is its duration still needs a name, and the
		// name has to say that is what it is -- "this round finished: success"
		// over a round that did not finish is how this was read wrong.
		//
		// Scoped to the wording map, not the file: these stages are keys in two
		// maps, and asking the file whether the key exists is a question the
		// other map can answer. That is how the provenance version of this check
		// passed on wording that had been renamed away.
		if !strings.Contains(stageWording(t, body), stage+": '") {
			t.Errorf("STAGE_TEXT has no wording for stage %q", stage)
		}
	}
	for stage := range listed {
		known := false
		for _, declared := range observability.DurationOnlyStages {
			if declared == stage {
				known = true
			}
		}
		if !known {
			t.Errorf("the page suppresses the outcome on stage %q, which is not a duration-only "+
				"stage: a real result is being hidden", stage)
		}
	}
}

// The container check runs one way: it finds ids in the markup and looks for a
// script that writes them. The other direction was open, and it is the one that
// throws -- text() and show() call getElementById(id).something, so a script
// addressing an id the markup does not declare does not render a blank panel,
// it stops the whole render at that line and leaves every panel after it as it
// was on the previous refresh.
func TestEveryElementTheScriptAddressesIsDeclaredInTheMarkup(t *testing.T) {
	body := string(page)
	declared := map[string]bool{}
	for _, match := range regexp.MustCompile(`id="([A-Za-z0-9_]+)"`).FindAllStringSubmatch(body, -1) {
		declared[match[1]] = true
	}
	if len(declared) == 0 {
		t.Fatal("no element ids found in the markup; the check would pass vacuously")
	}
	// The three ways the page addresses an element by a literal id.
	addressed := regexp.MustCompile(`(?:getElementById|\btext|\bshow|\bfail)\('([A-Za-z0-9_]+)'`)
	matches := addressed.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("the page addresses no element ids; the check would pass vacuously")
	}
	for _, match := range matches {
		if !declared[match[1]] {
			t.Errorf("a script addresses element %q, which the markup does not declare: "+
				"the call throws and the rest of that render never happens", match[1])
		}
	}
}

// assertFieldsExist checks every `<object>.<field>` the page reads against the
// JSON the Go type actually sends.
func assertFieldsExist(t *testing.T, object string, response reflect.Type) {
	t.Helper()
	sent := map[string]bool{}
	var collect func(reflect.Type)
	collect = func(structType reflect.Type) {
		for index := 0; index < structType.NumField(); index++ {
			field := structType.Field(index)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			// An embedded struct with no tag is flattened into the same JSON
			// object, so its fields are sent under this name too. Without this
			// the check reports every one of them as missing, which is how the
			// list response ended up with no check at all.
			if field.Anonymous && name == "" {
				embedded := field.Type
				if embedded.Kind() == reflect.Ptr {
					embedded = embedded.Elem()
				}
				if embedded.Kind() == reflect.Struct {
					collect(embedded)
					continue
				}
			}
			if name != "" && name != "-" {
				sent[name] = true
			}
		}
	}
	collect(response)
	if len(sent) == 0 {
		t.Fatalf("no JSON fields found on %s; the check would pass vacuously", response.Name())
	}

	read := regexp.MustCompile(`\b` + object + `\.([a-z0-9_]+)`)
	matches := read.FindAllStringSubmatch(string(page), -1)
	if len(matches) == 0 {
		t.Fatalf("the page reads no %s fields; the check would pass vacuously", object)
	}
	for _, match := range matches {
		if !sent[match[1]] {
			t.Errorf("the page reads %s.%s, which %s does not send: it reads as undefined and renders nothing",
				object, match[1], response.Name())
		}
	}
}
