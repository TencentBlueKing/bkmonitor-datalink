// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// A closed label set folds a word it does not name onto its "other" value,
// so that a label stays bounded whatever its input. That fold is for words
// from outside - a store, a broker, a newer peer - and not for the words this
// build writes itself: a word the code emits and its own set leaves out is
// rewritten on every line and every cell, and reads as an unclassified
// emitter where the site had said exactly what happened. A supplement whose
// kept read was absent wrote result=skipped and every reader saw _other.
//
// The test below reads every non-test file of alarmd for the values that
// reach each set - constants and literals put in its field by a composite
// literal or an assignment, through a local name bound to one, through a
// parameter from each call that passes it, through the return of a function
// of the module, and through a field of a struct from every write of that
// field - and fails on any value the set's own normaliser would fold. A
// name is the declaration visible where it is read; a local's field is what
// the statements before the read last wrote; and a read inside a branch on
// the very expression it reads (if x == A, if x != "", a switch on x, a
// result the site replaces when the call's ok is false) keeps only the
// values that branch admits.
//
// Where the source does not say what a value is - a map lookup, a value
// decoded from a store, a word handed through a func value - the scan cannot
// check it, and it does not skip it quietly either: every such expression is
// listed in closedLabelSetUnresolved, and a new one fails.

// labelSetFoldAllowed lists values the scan finds reaching a set that are
// not in it, by set and value, each with why it is not a fold.
var labelSetFoldAllowed = map[string]string{
	// (c) folded by design. LevelOutcomeReasonLabel keeps a Level outcome's
	// reason as the level_outcome_total label only when it is a coverage
	// reason, to keep that family bounded; the log line keeps the exact
	// reason, and storage failures are counted by their own State and Redis
	// families. Bounded by TestLevelOutcomeReasonIsExactOnTheLineAndFoldedOnTheMetric.
	"level_outcome_reason|BACKEND_CAPABILITY_MISSING": "(c) folded by design: LevelOutcomeReasonLabel",
	"level_outcome_reason|GAPPED":                     "(c) folded by design: LevelOutcomeReasonLabel",
	"level_outcome_reason|REDIS_UNAVAILABLE":          "(c) folded by design: LevelOutcomeReasonLabel",
	"level_outcome_reason|STATE_BUDGET_EXCEEDED":      "(c) folded by design: LevelOutcomeReasonLabel",
	"level_outcome_reason|STATE_CORRUPT":              "(c) folded by design: LevelOutcomeReasonLabel",
	"level_outcome_reason|STATE_READ_DEADLINE":        "(c) folded by design: LevelOutcomeReasonLabel",
	"level_outcome_reason|STATE_READ_TIMEOUT":         "(c) folded by design: LevelOutcomeReasonLabel",
	"level_outcome_reason|STATE_SCHEMA_UNSUPPORTED":   "(c) folded by design: LevelOutcomeReasonLabel",
	"level_outcome_reason|WARMING":                    "(c) folded by design: LevelOutcomeReasonLabel",
	"level_outcome_reason|none":                       "(c) folded by design: LevelOutcomeReasonLabel",
	// (b) a gap scope that opens, strengthens or warms is refused by its
	// mutation's builder and digest check without a reason, and the scopes
	// a snapshot reports are only written from such mutations; FoldGapReason
	// returns empty only when every reason it folds is empty. Bounded by
	// TestAGapScopeThatIsNotAClearCannotBeBuiltWithoutAReason (execution).
	"gap_scope_reason|": "(b) TestAGapScopeThatIsNotAClearCannotBeBuiltWithoutAReason",
}

// labelSet is one closed set: where its values come in and what it keeps.
type labelSet struct {
	name   string
	fields []fieldSink
	params []paramSink
	known  func(string) bool
}

func closedLabelSets() []labelSet {
	const obs = "observability"
	isResult := func(v string) bool { return NormalizeResult(Result(v)) == Result(v) }
	isReason := func(v string) bool {
		// none and empty are the absence of a reason; which word that
		// reads as by result is the reasonless-lines test's concern.
		return v == "" || v == string(ReasonNone) || NormalizeReason(ReasonCode(v), ResultDegraded) == ReasonCode(v)
	}
	oneOf := func(values ...string) func(string) bool {
		return func(v string) bool {
			for _, value := range values {
				if v == value {
					return true
				}
			}
			return false
		}
	}
	failureCategories := oneOf(QueryFailureCategoryBudget, QueryFailureCategorySourceBackend,
		QueryFailureCategorySeriesIdentity, QueryFailureCategoryCompletionContract, QueryFailureCategoryNamedInput,
		QueryFailureCategoryProviderTransport, QueryFailureCategoryAdmission, QueryFailureCategoryEvaluation,
		QueryFailureCategoryDelivery, QueryFailureCategoryOther)
	stateAdmissionCalls := func(arg int) []paramSink {
		return []paramSink{{dir: "worker", recv: "SlotExecutionCoordinator", name: "observeChunk", arg: arg,
			when: map[int]string{1: StageStateAdmission}}}
	}
	statePreflightCalls := func(arg int) []paramSink {
		return []paramSink{{dir: "worker", recv: "SlotExecutionCoordinator", name: "observeWithCounts", arg: arg,
			when: map[int]string{2: StageStatePreflight}}}
	}
	return []labelSet{
		{name: "result", fields: []fieldSink{{"observability.Observation", "Result"}}, known: isResult},
		{
			// Each against the catalogue's own list; the pair is held by
			// NormalizeComponentStage, which folds an unlisted pair.
			name: "component", fields: []fieldSink{{"observability.Observation", "Component"}},
			known: func(v string) bool {
				for _, pair := range AllComponentStages() {
					if string(pair.Component) == v {
						return true
					}
				}
				return v == ComponentOther
			},
		},
		{
			name: "stage", fields: []fieldSink{{"observability.Observation", "Stage"}},
			known: func(v string) bool {
				for _, stage := range AllStages() {
					if string(stage) == v {
						return true
					}
				}
				return v == StageOther
			},
		},
		{
			// Empty is how a line that is not query_completed says it has
			// no evaluation outcome; normalizeEvaluationOutcome keeps it.
			name: "evaluation_result", fields: []fieldSink{{"observability.Observation", "EvaluationResult"}},
			known: func(v string) bool { return v == "" || isResult(v) },
		},
		{
			name: "operation", fields: []fieldSink{{"observability.Observation", "Operation"}},
			known: func(v string) bool { return v == "" || NormalizeOperation(Operation(v)) == Operation(v) },
		},
		{
			name: "reason",
			fields: []fieldSink{
				{"observability.Observation", "ReasonCode"}, {"observability.Observation", "EvaluationReason"},
				{"observability.Observation", "SlotCompletionReason"}, {"observability.Observation", "ProgressCompletionReason"},
				{"observability.CarriedReasonFacts", "OriginCause"},
			},
			params: []paramSink{{dir: obs, name: "ViewStreamReasonCode", arg: 0}},
			known:  isReason,
		},
		{
			name: "capacity_budget", fields: []fieldSink{{"observability.Observation", "CapacityBudget"}},
			known: func(v string) bool { return NormalizeCapacityBudget(CapacityBudget(v)) == CapacityBudget(v) },
		},
		{
			// NormalizeSourceKind folds an unknown kind to empty, not to
			// an other: the word is lost all the same.
			name: "source_kind", fields: []fieldSink{{"observability.Observation", "SourceKind"}},
			known: func(v string) bool { return v == "" || NormalizeSourceKind(SourceKind(v)) == SourceKind(v) },
		},
		{
			name: "completion_cause",
			fields: []fieldSink{
				{"observability.Observation", "ProgressCompletionCause"}, {"observability.Observation", "SlotCompletionCause"},
			},
			known: func(v string) bool { return v == "" || NormalizeProgressCompletionCause(v) == v },
		},
		{
			name: "cursor_refusal", fields: []fieldSink{{"observability.CursorAdvanceFacts", "Refusal"}},
			known: oneOf("", CursorRefusalProgressMissing, CursorRefusalRangeInFlight, CursorRefusalCursorMoved,
				CursorRefusalCASConflict, CursorRefusalOther),
		},
		{
			name: "level_outcome_reason", fields: []fieldSink{{"observability.LevelOutcomeFact", "Reason"}},
			known: func(v string) bool { return v == "" || LevelOutcomeReasonLabel(v) == v },
		},
		{
			name: "query_status_code", fields: []fieldSink{{"observability.QueryStatusFacts", "Code"}},
			known: func(v string) bool { return v == "" || NormalizeQueryStatusCode(v) == v },
		},
		{
			name: "query_status_outcome", fields: []fieldSink{{"observability.QueryStatusFacts", "Outcome"}},
			known: oneOf(QueryStatusOutcomeAllowed, QueryStatusOutcomeUnavailable, QueryStatusOutcomeOther),
		},
		{
			name: "readiness_boundary", fields: []fieldSink{{"observability.SlotReadinessFacts", "Boundary"}},
			known: oneOf(ReadinessBoundaryUnified, ReadinessBoundaryMixed, ReadinessBoundaryNone, ReadinessBoundaryOther),
		},
		{
			name: "readiness_basis", fields: []fieldSink{{"observability.ReadinessDeferralFacts", "Basis"}},
			known: oneOf(ReadinessBasisSettleWait, ReadinessBasisReadHold, ReadinessBasisOther),
		},
		{
			name: "query_unavailable_attribution", fields: []fieldSink{{"observability.QueryUnavailableFacts", "Attribution"}},
			known: validQueryUnavailableAttribution,
		},
		{
			name: "query_http_class", fields: []fieldSink{{"observability.QueryUnavailableFacts", "HTTPClass"}},
			known: oneOf(QueryHTTPClass4xx, QueryHTTPClass5xx, QueryHTTPClassOther, QueryHTTPClassNone),
		},
		{
			name: "slot_skipped_path", fields: []fieldSink{{"observability.SlotSkippedFacts", "Path"}},
			known: func(v string) bool { return oneOf(SlotSkippedPaths...)(v) || v == SlotSkippedOther },
		},
		{
			name: "query_failure_stage", fields: []fieldSink{{"observability.QueryFailureFacts", "Stage"}},
			known: oneOf(QueryFailureStageExecute, QueryFailureStageStreamComplete, QueryFailureStageProvider,
				QueryFailureStageOther),
		},
		{
			name: "query_failure_category", fields: []fieldSink{{"observability.QueryFailureFacts", "Category"}},
			known: failureCategories,
		},
		{
			name: "query_failure_code", fields: []fieldSink{{"observability.QueryFailureFacts", "Code"}, {"observability.QueryRejection", "Class"}},
			known: func(v string) bool { return NormalizeQueryFailureCode(v) == v },
		},
		{
			name:   "state_write_reuse_class",
			params: []paramSink{{dir: obs, recv: "StateWriteReuseFacts", name: "Record", arg: 0}},
			known: func(v string) bool {
				return NormalizeStateWriteReuseClass(StateWriteReuseClass(v)) == StateWriteReuseClass(v)
			},
		},
		{
			name:   "state_write_reuse_stored",
			params: []paramSink{{dir: obs, recv: "StateWriteReuseFacts", name: "Record", arg: 1}},
			known: func(v string) bool {
				return NormalizeStateWriteReuseStored(StateWriteReuseStored(v)) == StateWriteReuseStored(v)
			},
		},
		{
			name:   "state_already_applied_kind",
			params: []paramSink{{dir: obs, recv: "StateAlreadyAppliedFacts", name: "Record", arg: 1}},
			known: func(v string) bool {
				return NormalizeStateAlreadyAppliedKind(StateAlreadyAppliedKind(v)) == StateAlreadyAppliedKind(v)
			},
		},
		{
			name:   "state_version_conflict_kind",
			params: []paramSink{{dir: obs, recv: "StateVersionConflictFacts", name: "Record", arg: 1}},
			known: func(v string) bool {
				return NormalizeStateVersionConflictKind(StateVersionConflictKind(v)) == StateVersionConflictKind(v)
			},
		},
		{
			name:   "state_admission_result",
			params: stateAdmissionCalls(5),
			known:  func(v string) bool { r, _ := NormalizeStateAdmission(Result(v), ""); return r == Result(v) },
		},
		{
			name:   "state_admission_reason",
			params: stateAdmissionCalls(6),
			known: func(v string) bool {
				_, r := NormalizeStateAdmission("", ReasonCode(v))
				return v == "" || v == string(ReasonNone) || r == ReasonCode(v)
			},
		},
		{
			name:   "state_preflight_result",
			params: statePreflightCalls(5),
			known: func(v string) bool {
				r, _ := NormalizeStatePreflight(Result(v), "")
				return v == "" || r == Result(v)
			},
		},
		{
			name:   "state_preflight_reason",
			params: statePreflightCalls(6),
			known: func(v string) bool {
				_, r := NormalizeStatePreflight("", ReasonCode(v))
				return v == "" || v == string(ReasonNone) || r == ReasonCode(v)
			},
		},
		{
			name: "wire_format",
			fields: []fieldSink{
				{"observability.OutputRejectedEvent", "Format"}, {"observability.OutputWithoutMessage", "Format"}, {"observability.OutputEventKindKey", "Format"},
			},
			params: []paramSink{{dir: obs, name: "NormalizeWireFormat", arg: 0}},
			known:  func(v string) bool { return NormalizeWireFormat(v) == v },
		},
		{
			name:   "output_event_kind",
			fields: []fieldSink{{"observability.OutputWithoutMessage", "EventKind"}, {"observability.OutputEventKindKey", "EventKind"}},
			params: []paramSink{{dir: obs, name: "NormalizeOutputEventKind", arg: 0}},
			known:  func(v string) bool { return NormalizeOutputEventKind(v) == v },
		},
		{
			name:   "output_reject_rule",
			fields: []fieldSink{{"observability.OutputRejectedEvent", "Rule"}, {"linkdoutput.RejectedError", "Rule"}},
			params: []paramSink{{dir: obs, name: "NormalizeOutputRejectRule", arg: 0}},
			known:  func(v string) bool { return NormalizeOutputRejectRule(v) == v },
		},
		{
			name:   "gap_scope_reason",
			fields: []fieldSink{{"observability.GapProgressFacts", "Reason"}},
			params: []paramSink{{dir: "contract", name: "NormalizeGapScopeReason", arg: 0}},
			known:  closedSetMember("contract", "GapScopeReasons"),
		},
		{
			// detection_snapshots_total counts a result the list names and
			// leaves any other uncounted.
			name:   "detection_snapshot_result",
			fields: []fieldSink{{"trigger.DetectionSnapshotFacts", "Result"}},
			known:  closedSetMember("contract", "DetectionSnapshotResults"),
		},
		{
			// detection_snapshot_states_total folds a state the list does
			// not name to _other.
			name:   "detection_snapshot_state",
			fields: []fieldSink{{"contract.DetectionRefValueV1", "State"}},
			known:  closedSetMember("metric", "DetectionSnapshotStates"),
		},
		{
			name:   "fleet_anomaly_kind",
			params: []paramSink{{dir: "fleet", name: "MetricKind", arg: 0}},
			known:  closedSetMember("fleet", "AnomalyKinds"),
		},
		{
			name:   "fleet_failure_category",
			params: []paramSink{{dir: "fleet", name: "MetricFailureCategory", arg: 0}},
			known:  failureCategories,
		},
	}
}

// closedSetMember reads a set this package cannot import from its source:
// the string constants a package-level slice var, or a function building
// one, lists.
func closedSetMember(dir, name string) func(string) bool {
	return func(v string) bool {
		for _, member := range closedSetMembers(dir, name) {
			if member == v {
				return true
			}
		}
		return false
	}
}

var closedSetMemberCache sync.Map

func closedSetMembers(dir, name string) []string {
	key := dir + "|" + name
	if cached, ok := closedSetMemberCache.Load(key); ok {
		return cached.([]string)
	}
	index := scanIndexValue
	resolver := newScanResolver(index, nil)
	var members []string
	literals := func(file *scanFile, node ast.Node) {
		ast.Inspect(node, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			for _, element := range literal.Elts {
				members = append(members, resolver.values(&scanScope{file: file}, element, 0)...)
			}
			return false
		})
	}
	if def, ok := index.vars[dir][name]; ok {
		literals(def.file, def.expr)
	}
	for _, function := range index.functions[dir+"|"+name] {
		literals(function.file, function.decl.Body)
	}
	closedSetMemberCache.Store(key, members)
	return members
}

func TestEveryValueTheCodeWritesIsInItsClosedLabelSet(t *testing.T) {
	index := alarmdScanIndex(t)
	found := map[string]string{}
	for _, set := range closedLabelSets() {
		values, unresolved, at := index.collect(set)
		if len(values) == 0 && len(unresolved) == 0 {
			t.Errorf("%s: the scan found no value and nothing it could not read; it is reading nothing", set.name)
		}
		checkLabelSet(t, set.name, values, set.known)
		for key := range unresolved {
			found[set.name+"|"+key] = at[key]
		}
	}
	keys := make([]string, 0, len(found)+len(closedLabelSetUnresolved))
	for key := range found {
		keys = append(keys, key)
	}
	for key := range closedLabelSetUnresolved {
		if _, seen := found[key]; !seen {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		where, seen := found[key]
		_, listed := closedLabelSetUnresolved[key]
		switch {
		case seen && !listed:
			t.Errorf("%s (at %s): a value the scan cannot read reaches a closed set: make it one the source says, "+
				"or list it with what bounds it", key, where)
		case listed && !seen:
			t.Errorf("%s: listed as unreadable and no longer reached: remove it", key)
		}
	}
}

// A Level outcome's reason outside the coverage list is folded to other on
// level_outcome_total, by design, and kept exact on the evaluation line.
func TestLevelOutcomeReasonIsExactOnTheLineAndFoldedOnTheMetric(t *testing.T) {
	const reason = "STATE_CORRUPT"
	if got := LevelOutcomeReasonLabel(reason); got != LevelOutcomeReasonOther {
		t.Fatalf("LevelOutcomeReasonLabel(%s) = %q, want %q", reason, got, LevelOutcomeReasonOther)
	}
	var output bytes.Buffer
	renderTo(&output, Observation{
		Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultSuccess,
		LevelOutcomes: []LevelOutcomeFact{{Outcome: "UNKNOWN", Reason: reason, Count: 3}},
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode evaluation line: %v; log=%s", err, output.String())
	}
	outcomes, ok := event["level_outcomes"].([]any)
	if !ok || len(outcomes) != 1 {
		t.Fatalf("level_outcomes = %#v; log=%s", event["level_outcomes"], output.String())
	}
	if got := outcomes[0].(map[string]any)["reason"]; got != reason {
		t.Fatalf("level outcome reason on the line = %#v, want %q", got, reason)
	}
}

// The evaluation line names the recoveries zeros withheld on its strategy's
// open alerts: the counter has only the reason, and this is where a reader
// finds which strategy it is.
func TestTheEvaluationLineCarriesTheWithheldRecoveries(t *testing.T) {
	var output bytes.Buffer
	renderTo(&output, Observation{
		Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultSuccess,
		Trace:            TraceFields{StrategyID: "1001", BusinessID: "2"},
		RecoveryWithheld: []RecoveryWithheldFact{{Reason: RecoveryWithheldZeroCount, Levels: 2}},
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode evaluation line: %v; log=%s", err, output.String())
	}
	withheld, ok := event["recovery_withheld"].([]any)
	if !ok || len(withheld) != 1 || withheld[0].(map[string]any)["reason"] != string(RecoveryWithheldZeroCount) {
		t.Fatalf("recovery_withheld = %#v; log=%s", event["recovery_withheld"], output.String())
	}
	if event["strategy_id"] != "1001" {
		t.Fatalf("the line does not name its strategy: %s", output.String())
	}
}

// The lines that wrote result=skipped and were read as _other - the
// supplement whose kept read was absent, the Slot held for a supplement or
// for maintenance - keep their word; so does the control plane's recovery,
// with no special case of its own.
func TestResultsTheCodeWritesNormalizeToThemselves(t *testing.T) {
	for _, result := range []Result{ResultSkipped, ResultRecovered} {
		if got := NormalizeResult(result); got != result {
			t.Errorf("NormalizeResult(%q) = %q", result, got)
		}
		for _, component := range []Component{ComponentControlPlane, ComponentScheduler} {
			observation := NormalizeObservation(Observation{
				Component: component, Stage: StageSlotCompleted, Result: result, ReasonCode: ReasonInternalUnknown,
			})
			if observation.Result != result {
				t.Errorf("%s %q normalized to %q", component, result, observation.Result)
			}
		}
	}
	index := alarmdScanIndex(t)
	values, _, _ := index.collect(labelSet{name: "result", fields: []fieldSink{{"observability.Observation", "Result"}}})
	seen := map[string]bool{}
	for _, value := range values {
		seen[value.value] = true
	}
	for _, want := range []string{ResultSkipped, ResultRecovered} {
		if !seen[want] {
			t.Errorf("the scan no longer sees %q reach Observation.Result; it has stopped reading the sites that write it", want)
		}
	}
}

// labelValue is one value found to reach a set, and the first place it was
// found.
type labelValue struct {
	value string
	at    string
}

func checkLabelSet(t *testing.T, set string, values []labelValue, known func(string) bool) {
	t.Helper()
	for _, found := range values {
		if known(found.value) {
			continue
		}
		if _, allowed := labelSetFoldAllowed[set+"|"+found.value]; allowed {
			continue
		}
		t.Errorf("%s %q, reaching the set at %s, is not in it: every line and cell would read it as the other value",
			set, found.value, found.at)
	}
	if testing.Verbose() {
		names := make([]string, 0, len(values))
		for _, found := range values {
			names = append(names, strconv.Quote(found.value))
		}
		t.Logf("%s: %d values checked: %s", set, len(values), strings.Join(names, " "))
	}
}

// fieldSink is a struct field a value reaches a set through: a composite
// literal of the type keyed by the field, or an assignment to the field of a
// name or field chain of that type.
type fieldSink struct {
	typ   string
	field string
}

// paramSink is a parameter a value reaches a set through: what every call of
// the function passes there. recv is the receiver's type name, empty for a
// function; when keeps only the calls whose argument at a position is that
// constant.
type paramSink struct {
	dir  string
	recv string
	name string
	arg  int
	when map[int]string
}

type scanFile struct {
	dir     string
	path    string
	file    *ast.File
	imports map[string]string
}

type scanDef struct {
	file *scanFile
	expr ast.Expr
}

type scanFunc struct {
	file *scanFile
	decl *ast.FuncDecl
}

type scanCall struct {
	file   *scanFile
	caller *scanFunc
	call   *ast.CallExpr
}

type scanWrite struct {
	scope *scanScope
	expr  ast.Expr
	// zero is a field a literal leaves out: it holds the zero value, at
	// the literal.
	zero bool
	at   token.Pos
	// filled is a write whose empty value something fills before it is
	// normalised (observationFillers, or an assignment to the field of the
	// name the literal is bound to).
	filled bool
}

// observationClosedFields are the closed-set fields of an Observation the
// scan holds a literal to when it leaves them out: the empty value must be
// one the set keeps, or something must fill it. ReasonCode has its own rule
// (TestEveryLineThatIsNotASuccessNamesItsReason).
var observationClosedFields = []string{"Result", "Stage", "Component", "Operation"}

// observationFillers are the functions that fill an Observation field left
// empty before it is normalised, by package directory, receiver type and
// name, with the fields each fills. Each is held to it by a test of its own:
// TestEmitObservationFillsAnEmptyResult (worker).
var observationFillers = map[string][]string{
	"worker|SlotExecutionCoordinator|emitObservation": {"Result"},
}

type scanIndex struct {
	fset      *token.FileSet
	files     []*scanFile
	consts    map[string]map[string]scanDef
	vars      map[string]map[string]scanDef
	types     map[string]map[string]bool
	functions map[string][]*scanFunc
	methods   map[string][]*scanFunc
	enclosing map[*ast.FuncDecl]*scanFunc
	// fields is each struct type's field types, by type name.
	fields map[string]map[string]string
	// calls is every call in the module, by the name it calls.
	calls map[string][]scanCall
	// writes is every write of a struct field, by type and field.
	writes map[string][]scanWrite
	locals map[*ast.FuncDecl]map[string]string
}

var (
	scanIndexOnce  sync.Once
	scanIndexValue *scanIndex
	scanIndexErr   error
)

func alarmdScanIndex(t *testing.T) *scanIndex {
	t.Helper()
	scanIndexOnce.Do(func() { scanIndexValue, scanIndexErr = buildScanIndex("..") })
	if scanIndexErr != nil {
		t.Fatal(scanIndexErr)
	}
	return scanIndexValue
}

func buildScanIndex(root string) (*scanIndex, error) {
	module, err := modulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	index := &scanIndex{
		fset:      token.NewFileSet(),
		consts:    map[string]map[string]scanDef{},
		vars:      map[string]map[string]scanDef{},
		types:     map[string]map[string]bool{},
		functions: map[string][]*scanFunc{},
		methods:   map[string][]*scanFunc{},
		enclosing: map[*ast.FuncDecl]*scanFunc{},
		fields:    map[string]map[string]string{},
		calls:     map[string][]scanCall{},
		writes:    map[string][]scanWrite{},
		locals:    map[*ast.FuncDecl]map[string]string{},
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, parseErr := parser.ParseFile(index.fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		relative, _ := filepath.Rel(root, path)
		scanned := &scanFile{
			dir: filepath.ToSlash(filepath.Dir(relative)), path: filepath.ToSlash(relative),
			file: file, imports: map[string]string{},
		}
		for _, spec := range file.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			if importPath != module && !strings.HasPrefix(importPath, module+"/") {
				continue
			}
			dir := strings.TrimPrefix(strings.TrimPrefix(importPath, module), "/")
			if dir == "" {
				dir = "."
			}
			alias := importPath[strings.LastIndex(importPath, "/")+1:]
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			scanned.imports[alias] = dir
		}
		index.files = append(index.files, scanned)
		index.add(scanned)
		return nil
	})
	if err != nil {
		return nil, err
	}
	index.indexWrites()
	return index, nil
}

func modulePath(goMod string) (string, error) {
	content, err := os.ReadFile(goMod)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", os.ErrNotExist
}

func (index *scanIndex) add(file *scanFile) {
	put := func(table map[string]map[string]scanDef, name string, expr ast.Expr) {
		if table[file.dir] == nil {
			table[file.dir] = map[string]scanDef{}
		}
		table[file.dir][name] = scanDef{file: file, expr: expr}
	}
	for _, declaration := range file.file.Decls {
		switch typed := declaration.(type) {
		case *ast.GenDecl:
			for _, spec := range typed.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if index.types[file.dir] == nil {
						index.types[file.dir] = map[string]bool{}
					}
					index.types[file.dir][spec.Name.Name] = true
					if spec.Assign.IsValid() {
						scanTypeAliases[file.dir+"."+spec.Name.Name] = scanAlias{file: file, expr: spec.Type}
					}
					structType, ok := spec.Type.(*ast.StructType)
					if !ok {
						continue
					}
					owner := file.dir + "." + spec.Name.Name
					if index.fields[owner] == nil {
						index.fields[owner] = map[string]string{}
					}
					for _, field := range structType.Fields.List {
						for _, name := range field.Names {
							index.fields[owner][name.Name] = qualifiedType(file, field.Type)
						}
						if len(field.Names) == 0 {
							embedded := qualifiedType(file, field.Type)
							index.fields[owner][embedded[strings.LastIndex(embedded, ".")+1:]] = embedded
						}
					}
				case *ast.ValueSpec:
					if len(spec.Values) != len(spec.Names) {
						continue
					}
					for i, name := range spec.Names {
						if typed.Tok == token.CONST {
							put(index.consts, name.Name, spec.Values[i])
						} else {
							put(index.vars, name.Name, spec.Values[i])
						}
					}
				}
			}
		case *ast.FuncDecl:
			function := &scanFunc{file: file, decl: typed}
			index.enclosing[typed] = function
			ast.Inspect(typed, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					if name := calleeName(call.Fun); name != "" {
						index.calls[name] = append(index.calls[name], scanCall{file: file, caller: function, call: call})
					}
				}
				return true
			})
			if typed.Recv == nil {
				key := file.dir + "|" + typed.Name.Name
				index.functions[key] = append(index.functions[key], function)
			} else {
				index.methods[typed.Name.Name] = append(index.methods[typed.Name.Name], function)
			}
		}
	}
}

// isNormaliser says a function is one of the folds themselves: what it
// writes, and what it is passed, is a value already read where it was first
// written, and reading it again here would only list the fold as a source.
func isNormaliser(function *scanFunc) bool {
	if function == nil {
		return false
	}
	return strings.HasPrefix(strings.ToLower(function.decl.Name.Name), "normalize")
}

// indexWrites records every write of a struct field: each keyed element of a
// composite literal of a named type, and each assignment to a field of a name
// or field chain whose type the source says.
func (index *scanIndex) indexWrites() {
	resolver := newScanResolver(index, nil)
	for _, file := range index.files {
		for _, declaration := range file.file.Decls {
			var function *scanFunc
			if decl, ok := declaration.(*ast.FuncDecl); ok {
				function = index.enclosing[decl]
			}
			if isNormaliser(function) {
				continue
			}
			scope := &scanScope{file: file, function: function}
			fills := index.observationFills(scope, declaration)
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.CompositeLit:
					typ := qualifiedType(file, node.Type)
					if index.fields[typ] == nil {
						return true
					}
					keyed := map[string]bool{}
					for _, element := range node.Elts {
						pair, ok := element.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						if key, ok := pair.Key.(*ast.Ident); ok {
							keyed[key.Name] = true
							write := scanWrite{scope: scope, expr: pair.Value, at: pair.Value.Pos(), filled: fills[node][key.Name]}
							index.writes[typ+"|"+key.Name] = append(index.writes[typ+"|"+key.Name], write)
						}
					}
					if typ == "observability.Observation" && len(node.Elts) == len(keyed) {
						for _, field := range observationClosedFields {
							if !keyed[field] && !fills[node][field] {
								write := scanWrite{scope: scope, zero: true, at: node.Pos()}
								index.writes[typ+"|"+field] = append(index.writes[typ+"|"+field], write)
							}
						}
					}
				case *ast.AssignStmt:
					if len(node.Lhs) != len(node.Rhs) || (node.Tok != token.ASSIGN && node.Tok != token.DEFINE) {
						return true
					}
					for i, left := range node.Lhs {
						selector, ok := left.(*ast.SelectorExpr)
						if !ok {
							continue
						}
						typ := resolver.typeOf(scope, selector.X)
						if typ == "" {
							continue
						}
						key := typ + "|" + selector.Sel.Name
						index.writes[key] = append(index.writes[key], scanWrite{scope: scope, expr: node.Rhs[i]})
					}
				}
				return true
			})
		}
	}
}

// observationFills is, for each Observation literal of one declaration, the
// fields filled after it: assigned through the name it is bound to, or filled
// by a filler it, or that name, is handed to.
func (index *scanIndex) observationFills(scope *scanScope, declaration ast.Decl) map[*ast.CompositeLit]map[string]bool {
	fills := map[*ast.CompositeLit]map[string]bool{}
	bound := map[string][]*ast.CompositeLit{}
	mark := func(literal *ast.CompositeLit, fields ...string) {
		if fills[literal] == nil {
			fills[literal] = map[string]bool{}
		}
		for _, field := range fields {
			fills[literal][field] = true
		}
	}
	observation := func(expression ast.Expr) (*ast.CompositeLit, bool) {
		literal, ok := unwrapLiteral(expression)
		return literal, ok && qualifiedType(scope.file, literal.Type) == "observability.Observation"
	}
	ast.Inspect(declaration, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.AssignStmt:
			if len(typed.Lhs) != len(typed.Rhs) {
				return true
			}
			for i, left := range typed.Lhs {
				if name, ok := left.(*ast.Ident); ok {
					if literal, ok := observation(typed.Rhs[i]); ok {
						bound[name.Name] = append(bound[name.Name], literal)
					}
				}
			}
		case *ast.ValueSpec:
			for i, name := range typed.Names {
				if i < len(typed.Values) {
					if literal, ok := observation(typed.Values[i]); ok {
						bound[name.Name] = append(bound[name.Name], literal)
					}
				}
			}
		}
		return true
	})
	resolver := newScanResolver(index, nil)
	ast.Inspect(declaration, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.AssignStmt:
			for _, left := range typed.Lhs {
				selector, ok := left.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if owner, ok := selector.X.(*ast.Ident); ok {
					for _, literal := range bound[owner.Name] {
						mark(literal, selector.Sel.Name)
					}
				}
			}
		case *ast.CallExpr:
			for _, callee := range resolver.callees(scope, typed) {
				receiver := ""
				if callee.decl.Recv != nil {
					receiver = types.ExprString(callee.decl.Recv.List[0].Type)
					receiver = strings.TrimPrefix(receiver, "*")
				}
				fields := observationFillers[callee.file.dir+"|"+receiver+"|"+callee.decl.Name.Name]
				if len(fields) == 0 {
					continue
				}
				for _, arg := range typed.Args {
					if literal, ok := observation(arg); ok {
						mark(literal, fields...)
					} else if name, ok := arg.(*ast.Ident); ok {
						for _, literal := range bound[name.Name] {
							mark(literal, fields...)
						}
					}
				}
			}
		}
		return true
	})
	return fills
}

// collect is every value that reaches a set, one entry per distinct value,
// sorted, and the expressions the scan could not read a value from.
func (index *scanIndex) collect(set labelSet) ([]labelValue, map[string]int, map[string]string) {
	sinks := map[string]bool{}
	for _, sink := range set.fields {
		sinks[sink.typ+"|"+sink.field] = true
	}
	resolver := newScanResolver(index, sinks)
	first := map[string]string{}
	record := func(values []string, at token.Pos) {
		for _, value := range values {
			if _, ok := first[value]; !ok {
				first[value] = index.fset.Position(at).String()
			}
		}
	}
	for _, sink := range set.fields {
		for _, write := range index.writes[sink.typ+"|"+sink.field] {
			if write.zero {
				record([]string{""}, write.at)
				continue
			}
			values := resolver.values(write.scope, write.expr, 0)
			if write.filled {
				values = slices.DeleteFunc(values, func(value string) bool { return value == "" })
			}
			record(values, write.at)
		}
	}
	for _, sink := range set.params {
		function := index.function(sink)
		if function == nil {
			resolver.unresolved["missing function "+sink.dir+"."+sink.recv+"."+sink.name]++
			continue
		}
		scope := &scanScope{file: function.file, function: function}
		parameter := parameterIdent(function.decl, sink.arg)
		resolver.when = sink.when
		record(resolver.callers(scope, parameter, sink.arg, 0), parameter.Pos())
		resolver.when = nil
	}
	values := make([]labelValue, 0, len(first))
	for value, at := range first {
		values = append(values, labelValue{value: value, at: at})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].value < values[j].value })
	return values, resolver.unresolved, resolver.where
}

func (index *scanIndex) function(sink paramSink) *scanFunc {
	candidates := index.functions[sink.dir+"|"+sink.name]
	if sink.recv != "" {
		candidates = index.methods[sink.name]
	}
	for _, candidate := range candidates {
		if candidate.file.dir != sink.dir {
			continue
		}
		if sink.recv == "" || qualifiedType(candidate.file, candidate.decl.Recv.List[0].Type) == sink.dir+"."+sink.recv {
			return candidate
		}
	}
	return nil
}

func parameterIdent(decl *ast.FuncDecl, position int) *ast.Ident {
	index := 0
	for _, field := range decl.Type.Params.List {
		for _, name := range field.Names {
			if index == position {
				return name
			}
			index++
		}
	}
	return decl.Name
}

// typedLocals are the names of one function whose type can be read from the
// source: the receiver, parameters and results, var declarations, names bound
// to a composite literal, to a call of a function of the module or to a field
// chain of a known type, and range values over one.
func (index *scanIndex) typedLocals(decl *ast.FuncDecl) map[string]string {
	if typed, ok := index.locals[decl]; ok {
		return typed
	}
	typed := map[string]string{}
	index.locals[decl] = typed
	function := index.enclosing[decl]
	resolver := newScanResolver(index, nil)
	scope := &scanScope{file: function.file, function: function}
	fieldList := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			for _, name := range field.Names {
				typed[name.Name] = qualifiedType(function.file, field.Type)
			}
		}
	}
	fieldList(decl.Recv)
	fieldList(decl.Type.Params)
	fieldList(decl.Type.Results)
	if decl.Body == nil {
		return typed
	}
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncLit:
			fieldList(node.Type.Params)
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if node.Type != nil {
					typed[name.Name] = qualifiedType(function.file, node.Type)
				} else if i < len(node.Values) {
					if literal, ok := unwrapLiteral(node.Values[i]); ok {
						typed[name.Name] = qualifiedType(function.file, literal.Type)
					}
				}
			}
		case *ast.AssignStmt:
			if node.Tok != token.DEFINE {
				return true
			}
			if len(node.Lhs) == len(node.Rhs) {
				for i, left := range node.Lhs {
					name, ok := left.(*ast.Ident)
					if !ok {
						continue
					}
					if call, ok := node.Rhs[i].(*ast.CallExpr); ok {
						if builtin, ok := call.Fun.(*ast.Ident); ok && builtin.Name == "make" && len(call.Args) > 0 {
							// make([]T, n) and make(map[K]T): ranged over or
							// indexed, they give a T.
							typed[name.Name] = qualifiedType(function.file, call.Args[0])
						} else if result := resolver.resultType(scope, call, 0); result != "" {
							typed[name.Name] = result
						}
					} else if known := resolver.typeOf(scope, node.Rhs[i]); known != "" {
						typed[name.Name] = known
					}
				}
			} else if index, ok := node.Rhs[0].(*ast.IndexExpr); ok && len(node.Rhs) == 1 && len(node.Lhs) == 2 {
				// value, found := m[key]: the value is the map's element.
				if name, ok := node.Lhs[0].(*ast.Ident); ok {
					if element := resolver.typeOf(scope, index); element != "" {
						typed[name.Name] = element
					}
				}
			} else if call, ok := node.Rhs[0].(*ast.CallExpr); ok && len(node.Rhs) == 1 {
				for i, left := range node.Lhs {
					if name, ok := left.(*ast.Ident); ok {
						if result := resolver.resultType(scope, call, i); result != "" {
							typed[name.Name] = result
						}
					}
				}
			}
		case *ast.RangeStmt:
			if name, ok := node.Value.(*ast.Ident); ok {
				if element := resolver.typeOf(scope, node.X); element != "" {
					typed[name.Name] = element
				}
			}
		}
		return true
	})
	return typed
}

// qualifiedType is the type a type expression names, as its package
// directory and name - observability.Observation for Observation in this
// package, *observability.Observation or observability.Observation in another
// - so that two packages' types of one name stay two types. A slice or a map
// reads as its element, which is what ranging over it gives; a predeclared
// type is its bare name.
func qualifiedType(file *scanFile, expression ast.Expr) string {
	name := namedType(file, expression)
	for range 8 {
		alias, ok := scanTypeAliases[name]
		if !ok {
			break
		}
		name = namedType(alias.file, alias.expr)
	}
	return name
}

// scanTypeAliases are the module's type aliases, by qualified name: an alias
// names the type it stands for.
var scanTypeAliases = map[string]scanAlias{}

type scanAlias struct {
	file *scanFile
	expr ast.Expr
}

func namedType(file *scanFile, expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.StarExpr:
		return namedType(file, typed.X)
	case *ast.SelectorExpr:
		if owner, ok := typed.X.(*ast.Ident); ok {
			if dir, imported := file.imports[owner.Name]; imported {
				return dir + "." + typed.Sel.Name
			}
		}
		return typed.Sel.Name
	case *ast.Ident:
		if types.Universe.Lookup(typed.Name) != nil {
			return typed.Name
		}
		return file.dir + "." + typed.Name
	case *ast.ArrayType:
		return namedType(file, typed.Elt)
	case *ast.MapType:
		return namedType(file, typed.Value)
	case *ast.Ellipsis:
		return namedType(file, typed.Elt)
	}
	return ""
}

func calleeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.SelectorExpr:
		return typed.Sel.Name
	case *ast.Ident:
		return typed.Name
	}
	return ""
}

type scanScope struct {
	file     *scanFile
	function *scanFunc
	// args are the arguments of the call the scan entered the function
	// through, by parameter position: a parameter read there is what that
	// call passed, not what every call passes.
	args map[int]scanArg
}

type scanArg struct {
	scope *scanScope
	expr  ast.Expr
}

type scanResolver struct {
	index *scanIndex
	// sinks are the fields of the set being read: a value read back from one
	// of them was counted where it was written.
	sinks map[string]bool
	when  map[int]string
	seen  map[string]bool
	// unresolved counts the expressions a value came from that the source
	// does not say the value of, by file, function and expression.
	unresolved map[string]int
	where      map[string]string
	// okGuard is the result position of a call whose false makes the site
	// replace the value it was asked for (x, ok := f(); if !ok { x = ... }),
	// or -1: the returns that say false there do not reach the site.
	okGuard int
}

func newScanResolver(index *scanIndex, sinks map[string]bool) *scanResolver {
	return &scanResolver{
		index: index, sinks: sinks, seen: map[string]bool{},
		unresolved: map[string]int{}, where: map[string]string{}, okGuard: -1,
	}
}

// replacedValues are the values a function replaces a name's value with
// something else when it holds them: if x == K { x = ... } drops K, as an
// empty format becoming WireFormatOther before it is counted does.
func (r *scanResolver) replacedValues(scope *scanScope, name string) map[string]bool {
	replaced := map[string]bool{}
	ast.Inspect(scope.function.decl.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.IfStmt)
		if !ok || statement.Init != nil || !assignsName(statement.Body, name) {
			return true
		}
		condition, ok := statement.Cond.(*ast.BinaryExpr)
		if !ok || condition.Op != token.EQL {
			return true
		}
		other := condition.Y
		if ident, ok := condition.Y.(*ast.Ident); ok && ident.Name == name {
			other = condition.X
		} else if ident, ok := condition.X.(*ast.Ident); !ok || ident.Name != name {
			return true
		}
		for _, value := range newScanResolver(r.index, nil).values(scope, other, 0) {
			replaced[value] = true
		}
		return true
	})
	return replaced
}

// assignsName says a block's own statements assign a name, unconditionally.
func assignsName(block *ast.BlockStmt, name string) bool {
	for _, statement := range block.List {
		assign, ok := statement.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for _, left := range assign.Lhs {
			if ident, ok := left.(*ast.Ident); ok && ident.Name == name {
				return true
			}
		}
	}
	return false
}

// okGuardPosition is, for x, ok := f() followed by if !ok { x = ... }, the
// position of ok among the call's results, or -1.
func okGuardPosition(body *ast.BlockStmt, assign *ast.AssignStmt, name string) int {
	if assign == nil {
		return -1
	}
	position := -1
	for index, left := range assign.Lhs {
		ok, isIdent := left.(*ast.Ident)
		if !isIdent || ok.Name == name || ok.Name == "_" {
			continue
		}
		ast.Inspect(body, func(node ast.Node) bool {
			statement, isIf := node.(*ast.IfStmt)
			if !isIf {
				return true
			}
			negation, isNot := statement.Cond.(*ast.UnaryExpr)
			if !isNot || negation.Op != token.NOT {
				return true
			}
			if guard, isIdent := negation.X.(*ast.Ident); isIdent && guard.Name == ok.Name && assignsName(statement.Body, name) {
				position = index
			}
			return true
		})
	}
	return position
}

// fieldWrite is one write of a local's field: an expression, one result of a
// call, or the zero value of a literal that leaves the field out.
type fieldWrite struct {
	expr   ast.Expr
	result int
	zero   bool
}

// reachingFieldWrites are the writes of name.field that can reach a read at
// pos, by statement order: walking back from the read through each enclosing
// block, a write that is one of those blocks' own statements is the last word
// and ends the walk; one nested in an earlier statement may or may not have
// run, so it is kept and the walk goes on. settled is false when the walk
// reaches the top of the function, or a binding of the name to something
// other than a composite literal, without such a write: the field then holds
// whatever its writes anywhere put there.
func reachingFieldWrites(body *ast.BlockStmt, name, field string, pos token.Pos) ([]fieldWrite, bool) {
	var path []ast.Node
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil || pos < node.Pos() || pos >= node.End() {
			return false
		}
		path = append(path, node)
		return true
	})
	var writes []fieldWrite
	for index := len(path) - 1; index >= 0; index-- {
		var list []ast.Stmt
		switch block := path[index].(type) {
		case *ast.BlockStmt:
			list = block.List
		case *ast.CaseClause:
			list = block.Body
		case *ast.CommClause:
			list = block.Body
		default:
			continue
		}
		for at := len(list) - 1; at >= 0; at-- {
			statement := list[at]
			if statement.Pos() > pos || (pos >= statement.Pos() && pos < statement.End()) {
				continue
			}
			own, kills, known := directFieldWrites(statement, name, field)
			if kills {
				return append(writes, own...), known
			}
			ast.Inspect(statement, func(node ast.Node) bool {
				if nested, ok := node.(ast.Stmt); ok && nested != statement {
					found, _, _ := directFieldWrites(nested, name, field)
					writes = append(writes, found...)
				}
				return true
			})
		}
	}
	return writes, false
}

// directFieldWrites are the writes one statement makes of name.field, whether
// it is the last word on the field (an assignment to it, or a new binding of
// the name), and, for a new binding, whether its value is read from the
// source: a binding to anything but a composite literal is not.
func directFieldWrites(statement ast.Stmt, name, field string) ([]fieldWrite, bool, bool) {
	var lefts, rights []ast.Expr
	switch typed := statement.(type) {
	case *ast.AssignStmt:
		lefts, rights = typed.Lhs, typed.Rhs
	case *ast.DeclStmt:
		general, ok := typed.Decl.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			return nil, false, false
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, ident := range value.Names {
				if ident.Name != name {
					continue
				}
				if len(value.Values) == 0 {
					// Filled through its address (errors.As, a decoder) or
					// a pointer: the declaration says nothing of the field.
					return nil, true, false
				}
				if index < len(value.Values) {
					return literalFieldWrite(value.Values[index], field)
				}
				return nil, true, false
			}
		}
		return nil, false, false
	default:
		return nil, false, false
	}
	for index, left := range lefts {
		switch target := left.(type) {
		case *ast.Ident:
			if target.Name != name {
				continue
			}
			if len(lefts) == len(rights) {
				return literalFieldWrite(rights[index], field)
			}
			return nil, true, false
		case *ast.SelectorExpr:
			owner, ok := target.X.(*ast.Ident)
			if !ok || owner.Name != name || target.Sel.Name != field {
				continue
			}
			if len(lefts) == len(rights) {
				return []fieldWrite{{expr: rights[index], result: -1}}, true, true
			}
			if len(rights) == 1 {
				return []fieldWrite{{expr: rights[0], result: index}}, true, true
			}
			return nil, true, false
		}
	}
	return nil, false, false
}

func literalFieldWrite(value ast.Expr, field string) ([]fieldWrite, bool, bool) {
	literal, ok := unwrapLiteral(value)
	if !ok {
		return nil, true, false
	}
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil, true, false
		}
		if key, ok := pair.Key.(*ast.Ident); ok && key.Name == field {
			return []fieldWrite{{expr: pair.Value, result: -1}}, true, true
		}
	}
	return []fieldWrite{{zero: true}}, true, true
}

// leaf records an expression the scan could not read a value from.
func (r *scanResolver) leaf(scope *scanScope, expression ast.Expr) []string {
	name := "-"
	if scope.function != nil {
		name = scope.function.decl.Name.Name
	}
	key := scope.file.path + "|" + name + "|" + types.ExprString(expression)
	r.unresolved[key]++
	if _, ok := r.where[key]; !ok {
		r.where[key] = r.index.fset.Position(expression.Pos()).String()
	}
	return nil
}

// maxScanDepth bounds how many hops - a bound name, a parameter, a return, a
// field - a value is followed through.
const maxScanDepth = 24

// values is every string constant an expression can be, as far as the source
// says without running it.
func (r *scanResolver) values(scope *scanScope, expression ast.Expr, depth int) []string {
	if expression == nil {
		return nil
	}
	if depth > maxScanDepth {
		return r.leaf(scope, expression)
	}
	key := r.index.fset.Position(expression.Pos()).String() + "#" + r.index.fset.Position(expression.End()).String()
	if r.seen[key] {
		return nil
	}
	r.seen[key] = true
	defer delete(r.seen, key)

	switch typed := expression.(type) {
	case *ast.ParenExpr:
		return r.values(scope, typed.X, depth)
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return r.leaf(scope, expression)
		}
		value, err := strconv.Unquote(typed.Value)
		if err != nil {
			return r.leaf(scope, expression)
		}
		return []string{value}
	case *ast.BinaryExpr:
		if typed.Op != token.ADD {
			return r.leaf(scope, expression)
		}
		var joined []string
		for _, left := range r.values(scope, typed.X, depth+1) {
			for _, right := range r.values(scope, typed.Y, depth+1) {
				joined = append(joined, left+right)
			}
		}
		return joined
	case *ast.CallExpr:
		if r.isConversion(scope, typed.Fun) && len(typed.Args) == 1 {
			return r.values(scope, typed.Args[0], depth)
		}
		return r.returns(scope, typed, 0, depth)
	case *ast.SelectorExpr:
		if owner, ok := typed.X.(*ast.Ident); ok {
			if dir, imported := scope.file.imports[owner.Name]; imported {
				if !r.isPackageValue(dir, typed.Sel.Name) {
					return r.leaf(scope, expression)
				}
				return r.packageValue(dir, typed.Sel.Name, depth)
			}
		}
		return r.guarded(scope, typed, r.field(scope, typed, depth))
	case *ast.Ident:
		return r.guarded(scope, typed, r.ident(scope, typed, depth))
	}
	return r.leaf(scope, expression)
}

// guarded keeps the values a read can have where it is: inside the branch of
// an if or a switch on the very expression it reads, only the values that
// branch admits, and after an if that returns on it, only what it lets
// through. A read in if x == A || x == B { ... } is A or B; one in
// if x != "" && ... { ... } is not empty; one in the default of a switch on x
// is none of its cases.
func (r *scanResolver) guarded(scope *scanScope, expression ast.Expr, values []string) []string {
	if len(values) == 0 || scope.function == nil || scope.function.decl.Body == nil {
		return values
	}
	text := types.ExprString(expression)
	constants := func(expressions ...ast.Expr) map[string]bool {
		found := map[string]bool{}
		for _, expression := range expressions {
			for _, value := range newScanResolver(r.index, nil).values(scope, expression, maxScanDepth-2) {
				found[value] = true
			}
		}
		return found
	}
	// terms splits a condition on op into comparisons of the expression with
	// a constant under compare, or reports that some term is not one.
	terms := func(condition ast.Expr, op, compare token.Token) ([]ast.Expr, bool) {
		var compared []ast.Expr
		var walk func(ast.Expr) bool
		walk = func(node ast.Expr) bool {
			if paren, ok := node.(*ast.ParenExpr); ok {
				return walk(paren.X)
			}
			binary, ok := node.(*ast.BinaryExpr)
			if !ok {
				return false
			}
			if binary.Op == op {
				return walk(binary.X) && walk(binary.Y)
			}
			if binary.Op != compare {
				return false
			}
			switch {
			case types.ExprString(binary.X) == text:
				compared = append(compared, binary.Y)
			case types.ExprString(binary.Y) == text:
				compared = append(compared, binary.X)
			default:
				return false
			}
			return true
		}
		return compared, walk(condition)
	}
	// excluded collects the terms of a conjunction that rule a value out,
	// ignoring the terms that say nothing about the expression.
	excluded := func(condition ast.Expr, op, compare token.Token) []ast.Expr {
		var out []ast.Expr
		var walk func(ast.Expr)
		walk = func(node ast.Expr) {
			if paren, ok := node.(*ast.ParenExpr); ok {
				walk(paren.X)
				return
			}
			binary, ok := node.(*ast.BinaryExpr)
			if !ok {
				return
			}
			if binary.Op == op {
				walk(binary.X)
				walk(binary.Y)
				return
			}
			if binary.Op == compare {
				if types.ExprString(binary.X) == text {
					out = append(out, binary.Y)
				} else if types.ExprString(binary.Y) == text {
					out = append(out, binary.X)
				}
			}
		}
		walk(condition)
		return out
	}
	var allowed []map[string]bool
	forbidden := map[string]bool{}
	pos := expression.Pos()
	inside := func(node ast.Node) bool { return node != nil && pos >= node.Pos() && pos < node.End() }
	// rebound reports a write to, or a new declaration of, the guarded
	// expression inside the branch before the read: from there the guard no
	// longer says what the expression holds.
	rebound := func(branch ast.Node) bool {
		found := false
		ast.Inspect(branch, func(node ast.Node) bool {
			if found || node == nil || node.Pos() >= pos {
				return false
			}
			switch typed := node.(type) {
			case *ast.AssignStmt:
				for _, left := range typed.Lhs {
					if types.ExprString(left) == text {
						found = true
					}
				}
			case *ast.ValueSpec:
				for _, name := range typed.Names {
					if name.Name == text {
						found = true
					}
				}
			}
			return !found
		})
		return found
	}
	// writtenSince reports a write to, or a new declaration of, the guarded
	// expression after a point and before the read.
	writtenSince := func(from token.Pos) bool {
		found := false
		ast.Inspect(scope.function.decl.Body, func(node ast.Node) bool {
			if found || node == nil || node.End() <= from || node.Pos() >= pos {
				return !found && node != nil && node.Pos() < pos
			}
			switch typed := node.(type) {
			case *ast.AssignStmt:
				for _, left := range typed.Lhs {
					if types.ExprString(left) == text && typed.Pos() >= from {
						found = true
					}
				}
			case *ast.ValueSpec:
				for _, name := range typed.Names {
					if name.Name == text && typed.Pos() >= from {
						found = true
					}
				}
			}
			return !found
		})
		return found
	}
	// returnsEarly is an if before the read, in a block the read is in, that
	// leaves the function when it holds: if x == "" { return }. The read
	// sees only what it lets through, unless x is written after it.
	returnsEarly := func(list []ast.Stmt) {
		for _, statement := range list {
			guard, ok := statement.(*ast.IfStmt)
			if !ok || guard.End() > pos || guard.Init != nil || guard.Else != nil || len(guard.Body.List) == 0 {
				continue
			}
			if _, leaves := guard.Body.List[len(guard.Body.List)-1].(*ast.ReturnStmt); !leaves || writtenSince(guard.End()) {
				continue
			}
			if compared, ok := terms(guard.Cond, token.LOR, token.EQL); ok {
				for value := range constants(compared...) {
					forbidden[value] = true
				}
			}
			if compared, ok := terms(guard.Cond, token.LAND, token.NEQ); ok {
				allowed = append(allowed, constants(compared...))
			}
		}
	}
	ast.Inspect(scope.function.decl.Body, func(node ast.Node) bool {
		if !inside(node) {
			return false
		}
		switch typed := node.(type) {
		case *ast.BlockStmt:
			returnsEarly(typed.List)
		case *ast.CaseClause:
			returnsEarly(typed.Body)
		case *ast.IfStmt:
			switch {
			case inside(typed.Body) && rebound(typed.Body):
			case inside(typed.Body):
				if compared, ok := terms(typed.Cond, token.LOR, token.EQL); ok {
					allowed = append(allowed, constants(compared...))
				}
				for value := range constants(excluded(typed.Cond, token.LAND, token.NEQ)...) {
					forbidden[value] = true
				}
			case typed.Else != nil && inside(typed.Else) && rebound(typed.Else):
			case typed.Else != nil && inside(typed.Else):
				if compared, ok := terms(typed.Cond, token.LAND, token.NEQ); ok {
					allowed = append(allowed, constants(compared...))
				}
				for value := range constants(excluded(typed.Cond, token.LOR, token.EQL)...) {
					forbidden[value] = true
				}
			}
		case *ast.SwitchStmt:
			if typed.Tag == nil || types.ExprString(typed.Tag) != text {
				return true
			}
			for _, statement := range typed.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if !ok || !inside(clause) || rebound(clause) {
					continue
				}
				if clause.List != nil {
					allowed = append(allowed, constants(clause.List...))
					continue
				}
				for _, other := range typed.Body.List {
					if otherClause, ok := other.(*ast.CaseClause); ok && otherClause.List != nil {
						for value := range constants(otherClause.List...) {
							forbidden[value] = true
						}
					}
				}
			}
		}
		return true
	})
	kept := values[:0:0]
	for _, value := range values {
		admitted := !forbidden[value]
		for _, set := range allowed {
			admitted = admitted && set[value]
		}
		if admitted {
			kept = append(kept, value)
		}
	}
	return kept
}

// field is every value written to a field, where the source says whose field
// it is and something writes it.
func (r *scanResolver) field(scope *scanScope, selector *ast.SelectorExpr, depth int) []string {
	if owner, ok := selector.X.(*ast.Ident); ok && scope.function != nil && scope.function.decl.Body != nil {
		if writes, settled := reachingFieldWrites(scope.function.decl.Body, owner.Name, selector.Sel.Name, selector.Pos()); settled {
			var values []string
			for _, write := range writes {
				switch {
				case write.zero:
					values = append(values, "")
				case write.result >= 0:
					call, _ := write.expr.(*ast.CallExpr)
					if call == nil {
						r.leaf(scope, selector)
						continue
					}
					values = append(values, r.returns(scope, call, write.result, depth+1)...)
				default:
					values = append(values, r.values(scope, write.expr, depth+1)...)
				}
			}
			return values
		}
	}
	return r.globalField(scope, selector, depth)
}

// globalField is every value written to a field anywhere, where the source
// says whose field it is and something writes it.
func (r *scanResolver) globalField(scope *scanScope, selector *ast.SelectorExpr, depth int) []string {
	owner := r.typeOf(scope, selector.X)
	if owner == "" {
		return r.leaf(scope, selector)
	}
	key := owner + "|" + selector.Sel.Name
	if r.sinks[key] {
		return nil
	}
	writes := r.index.writes[key]
	if len(writes) == 0 {
		return r.leaf(scope, selector)
	}
	var values []string
	for _, write := range writes {
		values = append(values, r.values(write.scope, write.expr, depth+1)...)
	}
	return values
}

func (r *scanResolver) isConversion(scope *scanScope, fun ast.Expr) bool {
	switch typed := fun.(type) {
	case *ast.ParenExpr:
		return r.isConversion(scope, typed.X)
	case *ast.Ident:
		return typed.Name == "string" || r.index.types[scope.file.dir][typed.Name]
	case *ast.SelectorExpr:
		owner, ok := typed.X.(*ast.Ident)
		if !ok {
			return false
		}
		dir, imported := scope.file.imports[owner.Name]
		return imported && r.index.types[dir][typed.Sel.Name]
	}
	return false
}

func (r *scanResolver) isPackageValue(dir, name string) bool {
	_, constant := r.index.consts[dir][name]
	_, variable := r.index.vars[dir][name]
	return constant || variable
}

func (r *scanResolver) packageValue(dir, name string, depth int) []string {
	if def, ok := r.index.consts[dir][name]; ok {
		return r.values(&scanScope{file: def.file}, def.expr, depth+1)
	}
	if def, ok := r.index.vars[dir][name]; ok {
		return r.values(&scanScope{file: def.file}, def.expr, depth+1)
	}
	return nil
}

func (r *scanResolver) ident(scope *scanScope, ident *ast.Ident, depth int) []string {
	if scope.function != nil {
		bound, declared := boundValues(scope.function.decl.Body, ident.Name, ident.Pos())
		replaced := r.replacedValues(scope, ident.Name)
		var values []string
		for _, binding := range bound {
			switch {
			case binding.zero:
				values = append(values, "")
			case binding.expr == nil:
				r.leaf(scope, ident)
			case binding.result < 0:
				values = append(values, r.values(scope, binding.expr, depth+1)...)
			default:
				call, _ := binding.expr.(*ast.CallExpr)
				if call == nil {
					r.leaf(scope, ident)
					continue
				}
				r.okGuard = okGuardPosition(scope.function.decl.Body, binding.assign, ident.Name)
				values = append(values, r.returns(scope, call, binding.result, depth+1)...)
				r.okGuard = -1
			}
		}
		if !declared {
			values = append(values, r.unbound(scope, ident, depth)...)
		}
		kept := values[:0]
		for _, value := range values {
			if !replaced[value] {
				kept = append(kept, value)
			}
		}
		return kept
	}
	return r.unbound(scope, ident, depth)
}

// unbound is what a name not declared in its function is: a parameter, or a
// package-level constant or variable.
func (r *scanResolver) unbound(scope *scanScope, ident *ast.Ident, depth int) []string {
	if scope.function != nil {
		if position, ok := parameterIndex(scope.function.decl, ident.Name); ok {
			if arg, entered := scope.args[position]; entered {
				return r.values(arg.scope, arg.expr, depth+1)
			}
			if isNormaliser(scope.function) {
				return nil
			}
			return r.callers(scope, ident, position, depth)
		}
	}
	if !r.isPackageValue(scope.file.dir, ident.Name) {
		return r.leaf(scope, ident)
	}
	return r.packageValue(scope.file.dir, ident.Name, depth)
}

// binding is one value a function gives a name: an expression of its own
// (result < 0), one result of a call, or nil where the source does not say
// (a range variable, a tuple from something other than a call).
type binding struct {
	expr   ast.Expr
	result int
	assign *ast.AssignStmt
	zero   bool
}

// boundValues is every value a function gives the name a read at pos
// refers to, and whether that name is declared in the function at all. A
// name declared again in an inner scope is another variable: its bindings
// count only for reads inside that scope, and an assignment counts for the
// declaration it assigns. Where no declaration in the function is visible at
// pos, the name is a parameter or a package-level name; the assignments to it
// are returned with declared false.
func boundValues(body *ast.BlockStmt, name string, pos token.Pos) ([]binding, bool) {
	type declaration struct {
		scope ast.Node
		pos   token.Pos
	}
	type assignment struct {
		binding
		pos token.Pos
	}
	var declarations []declaration
	var defined = map[token.Pos][]binding{}
	var assignments []assignment
	var stack []ast.Node
	innermost := func() ast.Node {
		if len(stack) == 0 {
			return body
		}
		return stack[len(stack)-1]
	}
	declare := func(at token.Pos, value binding) {
		scope := innermost()
		found := false
		for _, existing := range declarations {
			if existing.pos == at {
				found = true
			}
		}
		if !found {
			declarations = append(declarations, declaration{scope: scope, pos: at})
		}
		defined[at] = append(defined[at], value)
	}
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		switch typed := node.(type) {
		case *ast.AssignStmt:
			for i, left := range typed.Lhs {
				ident, ok := left.(*ast.Ident)
				if !ok || ident.Name != name {
					continue
				}
				value := binding{}
				switch {
				case len(typed.Lhs) == len(typed.Rhs):
					value = binding{expr: typed.Rhs[i], result: -1}
				case len(typed.Rhs) == 1:
					value = binding{expr: typed.Rhs[0], result: i, assign: typed}
				}
				if typed.Tok == token.DEFINE {
					declare(ident.Pos(), value)
				} else {
					assignments = append(assignments, assignment{binding: value, pos: ident.Pos()})
				}
			}
		case *ast.ValueSpec:
			for i, ident := range typed.Names {
				if ident.Name != name {
					continue
				}
				switch {
				case len(typed.Names) == len(typed.Values):
					declare(ident.Pos(), binding{expr: typed.Values[i], result: -1})
				case len(typed.Values) == 1:
					declare(ident.Pos(), binding{expr: typed.Values[0], result: i})
				case len(typed.Values) == 0:
					declare(ident.Pos(), binding{zero: true})
				default:
					declare(ident.Pos(), binding{})
				}
			}
		case *ast.RangeStmt:
			for _, variable := range []ast.Expr{typed.Key, typed.Value} {
				if ident, ok := variable.(*ast.Ident); ok && ident.Name == name {
					stack = append(stack, typed)
					if typed.Tok == token.DEFINE {
						declare(ident.Pos(), binding{})
					} else {
						assignments = append(assignments, assignment{pos: ident.Pos()})
					}
					stack = stack[:len(stack)-1]
				}
			}
		case *ast.FuncLit:
			stack = append(stack, typed)
			for _, field := range typed.Type.Params.List {
				for _, ident := range field.Names {
					if ident.Name == name {
						declare(ident.Pos(), binding{})
					}
				}
			}
			stack = stack[:len(stack)-1]
		}
		switch node.(type) {
		case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt,
			*ast.CaseClause, *ast.CommClause, *ast.SelectStmt, *ast.FuncLit:
			stack = append(stack, node)
		default:
			stack = append(stack, innermost())
		}
		return true
	})
	visible := func(at token.Pos) (token.Pos, bool) {
		best, bestSize, found := token.NoPos, token.Pos(0), false
		for _, candidate := range declarations {
			if candidate.pos > at || at < candidate.scope.Pos() || at >= candidate.scope.End() {
				continue
			}
			size := candidate.scope.End() - candidate.scope.Pos()
			if !found || size < bestSize || (size == bestSize && candidate.pos > best) {
				best, bestSize, found = candidate.pos, size, true
			}
		}
		return best, found
	}
	target, declared := visible(pos)
	var bound []binding
	if declared {
		bound = append(bound, defined[target]...)
	}
	for _, assigned := range assignments {
		owner, owned := visible(assigned.pos)
		if owned == declared && (!declared || owner == target) {
			bound = append(bound, assigned.binding)
		}
	}
	return bound, declared
}

func parameterIndex(decl *ast.FuncDecl, name string) (int, bool) {
	position := 0
	for _, field := range decl.Type.Params.List {
		if len(field.Names) == 0 {
			position++
			continue
		}
		for _, ident := range field.Names {
			if ident.Name == name {
				if _, variadic := field.Type.(*ast.Ellipsis); variadic {
					return 0, false
				}
				return position, true
			}
			position++
		}
	}
	return 0, false
}

// callers is what every call of a function passes as one of its parameters.
func (r *scanResolver) callers(scope *scanScope, parameter *ast.Ident, position, depth int) []string {
	function := scope.function
	when := r.when
	r.when = nil
	var values []string
	matched := 0
	for _, site := range r.index.calls[function.decl.Name.Name] {
		if position >= len(site.call.Args) || site.call.Ellipsis.IsValid() {
			continue
		}
		callerScope := &scanScope{file: site.file, function: site.caller}
		if !r.calls(callerScope, site.call.Fun, function) || !r.matches(callerScope, site.call, when) {
			continue
		}
		matched++
		values = append(values, r.values(callerScope, site.call.Args[position], depth+1)...)
	}
	if matched == 0 {
		// Called through a func value or an interface, or not at all.
		r.leaf(scope, parameter)
	}
	return values
}

func (r *scanResolver) matches(scope *scanScope, call *ast.CallExpr, when map[int]string) bool {
	for position, want := range when {
		if position >= len(call.Args) {
			return false
		}
		values := newScanResolver(r.index, nil).values(scope, call.Args[position], 0)
		if len(values) != 1 || values[0] != want {
			return false
		}
	}
	return true
}

// calls says whether a call's function expression can be a call of this
// function. A method is matched by its receiver's type where the source says
// what that is, and by its name only where one type alone has the method.
func (r *scanResolver) calls(scope *scanScope, fun ast.Expr, function *scanFunc) bool {
	switch fun := fun.(type) {
	case *ast.Ident:
		return function.decl.Recv == nil && scope.file.dir == function.file.dir
	case *ast.SelectorExpr:
		if owner, ok := fun.X.(*ast.Ident); ok {
			if dir, imported := scope.file.imports[owner.Name]; imported {
				return function.decl.Recv == nil && dir == function.file.dir
			}
		}
		if function.decl.Recv == nil || len(function.decl.Recv.List) == 0 {
			return false
		}
		receiver := qualifiedType(function.file, function.decl.Recv.List[0].Type)
		if known := r.typeOf(scope, fun.X); known != "" {
			return known == receiver
		}
		for _, other := range r.index.methods[function.decl.Name.Name] {
			if qualifiedType(other.file, other.decl.Recv.List[0].Type) != receiver {
				return false
			}
		}
		return true
	}
	return false
}

// typeOf is the type name of a name or a field chain, where the source says
// it: a local of a known type, then each field's declared type.
func (r *scanResolver) typeOf(scope *scanScope, expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.ParenExpr:
		return r.typeOf(scope, typed.X)
	case *ast.StarExpr:
		return r.typeOf(scope, typed.X)
	case *ast.UnaryExpr:
		return r.typeOf(scope, typed.X)
	case *ast.IndexExpr:
		return r.typeOf(scope, typed.X)
	case *ast.CompositeLit:
		return qualifiedType(scope.file, typed.Type)
	case *ast.Ident:
		if scope.function == nil {
			return ""
		}
		return r.index.typedLocals(scope.function.decl)[typed.Name]
	case *ast.SelectorExpr:
		owner := r.typeOf(scope, typed.X)
		if owner == "" {
			return ""
		}
		return r.index.fields[owner][typed.Sel.Name]
	}
	return ""
}

func (r *scanResolver) callees(scope *scanScope, call *ast.CallExpr) []*scanFunc {
	var candidates []*scanFunc
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		candidates = r.index.functions[scope.file.dir+"|"+fun.Name]
	case *ast.SelectorExpr:
		if owner, ok := fun.X.(*ast.Ident); ok {
			if dir, imported := scope.file.imports[owner.Name]; imported {
				candidates = r.index.functions[dir+"|"+fun.Sel.Name]
				break
			}
		}
		candidates = r.index.methods[fun.Sel.Name]
	}
	var callees []*scanFunc
	for _, candidate := range candidates {
		if r.calls(scope, call.Fun, candidate) {
			callees = append(callees, candidate)
		}
	}
	return callees
}

// resultType is the type name of one result of a call of a function of the
// module.
func (r *scanResolver) resultType(scope *scanScope, call *ast.CallExpr, result int) string {
	for _, callee := range r.callees(scope, call) {
		if callee.decl.Type.Results == nil {
			continue
		}
		position := 0
		for _, field := range callee.decl.Type.Results.List {
			count := max(len(field.Names), 1)
			if result < position+count {
				return qualifiedType(callee.file, field.Type)
			}
			position += count
		}
	}
	return ""
}

// returns is what a call to a function of the module can return as one of
// its results.
func (r *scanResolver) returns(scope *scanScope, call *ast.CallExpr, result, depth int) []string {
	guard := r.okGuard
	r.okGuard = -1
	var values []string
	matched := 0
	for _, callee := range r.callees(scope, call) {
		if callee.decl.Body == nil || callee.decl.Type.Results == nil {
			continue
		}
		results := callee.decl.Type.Results.NumFields()
		if result >= results {
			continue
		}
		matched++
		calleeScope := &scanScope{file: callee.file, function: callee}
		if !call.Ellipsis.IsValid() {
			calleeScope.args = map[int]scanArg{}
			for position, arg := range call.Args {
				calleeScope.args[position] = scanArg{scope: scope, expr: arg}
			}
		}
		ast.Inspect(callee.decl.Body, func(node ast.Node) bool {
			if _, closure := node.(*ast.FuncLit); closure {
				return false
			}
			ret, ok := node.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			switch {
			case len(ret.Results) == results && guard >= 0 && guard < results && isFalse(ret.Results[guard]):
			case len(ret.Results) == results:
				values = append(values, r.values(calleeScope, ret.Results[result], depth+1)...)
			case len(ret.Results) == 0:
				r.leaf(calleeScope, callee.decl.Name)
			default:
				r.leaf(calleeScope, ret.Results[0])
			}
			return true
		})
	}
	if matched == 0 {
		return r.leaf(scope, call)
	}
	return values
}

// closedLabelSetUnresolved is every expression a value comes into a closed
// set from that the scan cannot read, by set, file, function and expression.
// A new one fails, and so does one listed that is no longer reached.
//
// PENDING: each is still to be made readable by the scan, bounded by a test
// that enumerates what its producer can return, or shown to be outside any
// closed set. By kind:
//
//	B1 (19): a range variable or a slice or map element whose producers are other closed lists;
//	B2 (10): a map key or composite key of an output aggregation;
//	B3 (8): a call through an interface or a func value;
//	B4 (5): a value decoded from the store;
//	B5 (3): external or free text;
//	B6 (22): a field of a result whose type the scan does not see, or the result of a call it does not follow.
var closedLabelSetUnresolved = map[string]string{
	"capacity_budget|worker/sideeffect_reservation.go|checkEffectCounts|check.kind":                                                                                  "PENDING B6",
	"completion_cause|cmd/alarmd/runtime_phase_two_production.go|Execute|result.CompletionCause.Cause":                                                               "PENDING B6",
	"completion_cause|execution/model.go|deriveCompletionAttribution|candidate":                                                                                      "PENDING B1",
	"fleet_anomaly_kind|cmd/alarmd/runtime_phase_two_fleet.go|fleetVerdictOf|kind":                                                                                   "PENDING B1",
	"fleet_failure_category|cmd/alarmd/runtime_phase_two_fleet.go|fleetVerdictOf|category":                                                                           "PENDING B1",
	"gap_scope_reason|contract/gap_reason_fold.go|FoldGapReason|reason":                                                                                              "PENDING B1",
	"gap_scope_reason|metric/phase_two.go|observe|facts.Reason":                                                                                                      "PENDING B6",
	"gap_scope_reason|worker/stream_session.go|gapMutationForReasons|reasons[scope]":                                                                                 "PENDING B1",
	"level_outcome_reason|evaluation/evaluator.go|buildMutation|guarded":                                                                                             "PENDING B1",
	"level_outcome_reason|evaluation/evaluator.go|buildMutation|reason":                                                                                              "PENDING B1",
	"level_outcome_reason|evaluation/evaluator.go|evaluateRecordWith|f.ReasonCode":                                                                                   "PENDING B6",
	"level_outcome_reason|evaluation/evaluator.go|evaluateRecordWith|guarded":                                                                                        "PENDING B1",
	"level_outcome_reason|execution/gap_scope_reason.go|RoundGuardReasonForLevel|reason":                                                                             "PENDING B1",
	"level_outcome_reason|state/execution_v2_store.go|decodeRuntime|reason":                                                                                          "PENDING B4",
	"level_outcome_reason|worker/resume_applied_series.go|resumedSeriesResult|guard.ReasonCode":                                                                      "PENDING B6",
	"operation|cmd/alarmd/runtime_phase_two_production.go|AcquireQueryPermit|operation":                                                                              "PENDING B3",
	"operation|scheduler/slot_source.go|classifyRecovery|source.classifyRecoveryOf(ctx, evaluationTime, deadline, nil, hold, at, fence)":                             "PENDING B3",
	"output_event_kind|kafka/trigger_event_sink.go|WriteBatch|key.eventKind":                                                                                         "PENDING B2",
	"output_event_kind|metric/phase_two.go|observe|key.EventKind":                                                                                                    "PENDING B2",
	"output_event_kind|worker/coordinator.go|withoutMessageReported|k.kind":                                                                                          "PENDING B2",
	"output_event_kind|worker/coordinator.go|writeEvents|dropped.EventKind":                                                                                          "PENDING B2",
	"query_failure_code|access/uq/rejection.go|rejectionClass|strconv.Itoa(status)":                                                                                  "PENDING B5",
	"query_failure_code|worker/stream_session.go|physicalFailureReason|execution.AttributeUnavailable(facts, execution.ReasonCode(contract.ReasonQueryUnavailable))": "PENDING B6",
	"reason|cmd/alarmd/runtime_phase_two_production.go|Execute|result.CompletionCause.Reason":                                                                        "PENDING B6",
	"reason|cmd/alarmd/runtime_phase_two_production.go|Execute|result.ReasonCode":                                                                                    "PENDING B6",
	"reason|cmd/alarmd/slot_failure_reason.go|slotFailureReason|admission.QueryFailure()":                                                                            "PENDING B3",
	"reason|contract/gap_reason_fold.go|FoldGapReason|reason":                                                                                                        "PENDING B1",
	"reason|evaluation/evaluator.go|buildMutation|guarded":                                                                                                           "PENDING B1",
	"reason|evaluation/evaluator.go|buildMutation|reason":                                                                                                            "PENDING B1",
	"reason|evaluation/evaluator.go|evaluateRecordWith|f.ReasonCode":                                                                                                 "PENDING B6",
	"reason|evaluation/evaluator.go|evaluateRecordWith|guarded":                                                                                                      "PENDING B1",
	"reason|execution/gap_scope_reason.go|RoundGuardReasonForLevel|reason":                                                                                           "PENDING B1",
	"reason|execution/model.go|deriveCompletionAttribution|candidateReason":                                                                                          "PENDING B1",
	"reason|execution/unavailable_attribution.go|AttributeUnavailable|facts.Attempts[index].ReasonCode":                                                              "PENDING B1",
	"reason|scheduler/query_cooldown.go|restoreQueryCooldown|record.Reason":                                                                                          "PENDING B4",
	"reason|scheduler/slot_source.go|repairedLanding|repair.ReasonCode()":                                                                                            "PENDING B3",
	"reason|state/execution_v2_no_data.go|applyOneNoData|reason":                                                                                                     "PENDING B4",
	"reason|state/execution_v2_store.go|decodeRuntime|reason":                                                                                                        "PENDING B4",
	"reason|viewstream/client.go|reprobe|fmt.Sprintf(\"missing=%d probed=%t\", missing, probed)":                                                                     "PENDING B5",
	"reason|viewstream/client.go|serveOnce|client.discovery.Leader(ctx)":                                                                                             "PENDING B3",
	"reason|viewstream/client.go|session|body.Refusal.Reason":                                                                                                        "PENDING B5",
	"reason|viewstream/server.go|admit|server.admission.Admit(ctx, hello.WorkerId, hello.StreamToken)":                                                               "PENDING B3",
	"reason|worker/coordinator.go|Execute|begin.ReasonCode":                                                                                                          "PENDING B6",
	"reason|worker/coordinator.go|Execute|finalization.ReasonCode":                                                                                                   "PENDING B6",
	"reason|worker/coordinator.go|admitPlan|result.ReasonCode":                                                                                                       "PENDING B6",
	"reason|worker/coordinator.go|commitProgress|progress.ReasonCode":                                                                                                "PENDING B6",
	"reason|worker/coordinator.go|finalizePreparedWithGaps|planResult.ReasonCode":                                                                                    "PENDING B6",
	"reason|worker/coordinator.go|finalizePreparedWithGaps|reason":                                                                                                   "PENDING B1",
	"reason|worker/coordinator.go|firstStateAdmissionFailureReason|item.ReasonCode":                                                                                  "PENDING B6",
	"reason|worker/coordinator.go|firstStateApplyFailureReason|item.ReasonCode":                                                                                      "PENDING B6",
	"reason|worker/coordinator.go|outputRejectionReason|rejection.OutputRejectionReason()":                                                                           "PENDING B3",
	"reason|worker/no_data_skip_streak.go|noteNoDataStreaks|stream.noDataOutcomes[index]":                                                                            "PENDING B1",
	"reason|worker/query_failure.go|observeQueryFailure|diagnostic.QueryFailure()":                                                                                   "PENDING B3",
	"reason|worker/resume_applied_series.go|resumedSeriesResult|guard.ReasonCode":                                                                                    "PENDING B6",
	"reason|worker/stream_session.go|gapMutationForReasons|reasons[scope]":                                                                                           "PENDING B1",
	"reason|worker/stream_session.go|observeCompletionOnlyProbe|binding.ReasonCode":                                                                                  "PENDING B6",
	"reason|worker/stream_session.go|physicalFailureReason|execution.AttributeUnavailable(facts, execution.ReasonCode(contract.ReasonQueryUnavailable))":             "PENDING B6",
	"reason|worker/supplement.go|supplementAdmitted|result.ReasonCode":                                                                                               "PENDING B6",
	"result|cmd/alarmd/runtime_phase_two_production.go|Execute|result.Result":                                                                                        "PENDING B6",
	"state_admission_reason|worker/coordinator.go|firstStateAdmissionFailureReason|item.ReasonCode":                                                                  "PENDING B6",
	"state_preflight_reason|state/execution_v2_store.go|decodeRuntime|reason":                                                                                        "PENDING B4",
	"wire_format|kafka/output_partial.go|partialRejection|formats[index]":                                                                                            "PENDING B2",
	"wire_format|kafka/trigger_event_sink.go|WriteBatch|key.format":                                                                                                  "PENDING B2",
	"wire_format|metric/phase_two.go|observe|format":                                                                                                                 "PENDING B2",
	"wire_format|metric/phase_two.go|observe|key.Format":                                                                                                             "PENDING B2",
	"wire_format|worker/coordinator.go|withoutMessageReported|k.format":                                                                                              "PENDING B2",
	"wire_format|worker/coordinator.go|writeEvents|dropped.Format":                                                                                                   "PENDING B2",
}

func isFalse(expression ast.Expr) bool {
	ident, ok := expression.(*ast.Ident)
	return ok && ident.Name == "false"
}
