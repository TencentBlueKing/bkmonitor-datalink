// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every failure the cutover can return has a name.
//
// This is the whole point of the family. A cutover that says only that it
// failed leaves nothing to act on, and one failed about twice a minute for
// eleven hours while the fleet quietly stopped picking up published content.
func TestEveryCutoverFailureIsNamed(t *testing.T) {
	for name, test := range map[string]struct {
		err  error
		want string
	}{
		"the activation and the Segments disagree": {
			err:  fmt.Errorf("%w: a kept Query Group has a Plan without a current activation record", ErrActivationRecordMissing),
			want: CutoverReasonActivationRecordMissing,
		},
		"an open Segment is not in the state the cutover requires": {
			err: ErrScheduleConflict, want: CutoverReasonSegmentConflict,
		},
		"the compare-and-set lost": {
			err: ErrActivationConflict, want: CutoverReasonConflict,
		},
		"stored content does not hash to its name": {
			err: ErrCatalogObjectCorrupt, want: CutoverReasonDigestMismatch,
		},
		"a persisted snapshot will not decode": {
			err:  &PersistedSnapshotCorruptError{Err: errors.New("bad")},
			want: CutoverReasonDigestMismatch,
		},
		"content the cutover needs is not stored": {
			err: ErrCatalogObjectUnavailable, want: CutoverReasonUnavailable,
		},
		"there is no snapshot to advance from": {
			err: ErrSnapshotUnavailable, want: CutoverReasonUnavailable,
		},
		"the request is not a cutover": {
			err:  fmt.Errorf("%w: publication schedule activation is required", ErrCutoverRequest),
			want: CutoverReasonInvalidRequest,
		},
		"the store failed underneath": {
			err:  &ActivationDependencyIOError{Err: errors.New("dial tcp: connection refused")},
			want: CutoverReasonIO,
		},
		"a wrapped store failure": {
			err:  fmt.Errorf("persist schedule cutover: %w", &ActivationDependencyIOError{Err: errors.New("broken pipe")}),
			want: CutoverReasonIO,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := cutoverFailureReason(test.err); got != test.want {
				t.Fatalf("reason = %q, want %q", got, test.want)
			}
		})
	}

	if got := cutoverFailureReason(nil); got != "" {
		t.Fatalf("a cutover that did not fail has reason %q, want none", got)
	}
	if got := cutoverFailureReason(errors.New("something nobody named")); got != CutoverReasonOther {
		t.Fatalf("an unnamed failure = %q, want %q; other is where a new failure path with no name "+
			"must land, so that it is visible rather than folded into one that has a meaning",
			got, CutoverReasonOther)
	}
}

// The reasons the metric bounds itself by are the ones this file defines.
func TestEveryCutoverReasonIsListed(t *testing.T) {
	listed := map[string]bool{}
	for _, reason := range CutoverReasons {
		if listed[reason] {
			t.Fatalf("reason %q is listed twice", reason)
		}
		listed[reason] = true
	}

	// The reasons are found by reading the source rather than from a list kept
	// here, because a list kept here is one more place to forget -- and it was
	// already stale by one reason when this was written. A reason that is
	// declared, returned somewhere, and absent from CutoverReasons creates no
	// label at startup, so the first failure landing on it reports a series
	// nobody is watching for.
	fileSet := token.NewFileSet()
	declared := map[string]bool{}
	for _, name := range []string{"cutover_reason.go", "redis_runtime.go"} {
		parsed, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && strings.HasPrefix(identifier.Name, "CutoverReason") {
				declared[identifier.Name] = true
			}
			return true
		})
	}
	if len(declared) == 0 {
		t.Fatal("no reasons were found in the source, so this guard scans nothing")
	}
	byName := map[string]string{
		"CutoverReasonActivationRecordMissing": CutoverReasonActivationRecordMissing,
		"CutoverReasonTimelineMissing":         CutoverReasonTimelineMissing,
		"CutoverReasonOpenSegmentClosed":       CutoverReasonOpenSegmentClosed,
		"CutoverReasonOpenDigestMismatch":      CutoverReasonOpenDigestMismatch,
		"CutoverReasonLegacyRevisionMismatch":  CutoverReasonLegacyRevisionMismatch,
		"CutoverReasonSegmentContentMismatch":  CutoverReasonSegmentContentMismatch,
		"CutoverReasonSegmentConflict":         CutoverReasonSegmentConflict,
		"CutoverReasonDigestMismatch":          CutoverReasonDigestMismatch,
		"CutoverReasonObjectNewer":             CutoverReasonObjectNewer,
		"CutoverReasonConflict":                CutoverReasonConflict,
		"CutoverReasonUnavailable":             CutoverReasonUnavailable,
		"CutoverReasonInvalidRequest":          CutoverReasonInvalidRequest,
		"CutoverReasonIO":                      CutoverReasonIO,
		"CutoverReasonOther":                   CutoverReasonOther,
	}
	for name := range declared {
		if name == "CutoverReasons" {
			continue
		}
		value, known := byName[name]
		if !known {
			t.Fatalf("%s is used in the cutover and this guard does not know its value; add it here and "+
				"to CutoverReasons, or the metric will never create its label", name)
		}
		if !listed[value] {
			t.Fatalf("reason %s (%q) is used and not in CutoverReasons, so the metric never creates its "+
				"label and a failure landing there reports a series nobody can find", name, value)
		}
	}
}

// No failure path in the cutover returns a bare error.
//
// A bare errors.New cannot be told apart from any other by the reporter, so it
// lands in other and says nothing -- and other is meant to stay at zero. This
// scans the function rather than trusting a list, because the failure this
// guards is a path added later by someone who did not read this file.
func TestTheCutoverReturnsNoUnnamedError(t *testing.T) {
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "redis_runtime.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var target *ast.FuncDecl
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "CompareAndSetPublicationScheduleActivation" {
			target = function
		}
	}
	if target == nil {
		t.Fatal("CompareAndSetPublicationScheduleActivation is gone; this guard now scans nothing")
	}

	var bare []string
	ast.Inspect(target, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "errors" || selector.Sel.Name != "New" {
			return true
		}
		position := fileSet.Position(call.Pos())
		bare = append(bare, fmt.Sprintf("redis_runtime.go:%d", position.Line))
		return true
	})
	if len(bare) > 0 {
		t.Fatalf("the cutover returns %d unnamed error(s) at %s. A bare errors.New reaches the reporter "+
			"as other, which is the label that means 'a failure path nobody named' -- wrap it in one of "+
			"the sentinels this package defines, or add a sentinel for it",
			len(bare), strings.Join(bare, ", "))
	}
}

// A failed cutover reports its reason, the Query Group it stopped on, and the
// error itself.
//
// Computing the reason is not reporting it. The observation is the only thing
// a reader sees, and until this release it carried neither the reason nor the
// error -- a core action failing every round, visible as one label on a
// duration histogram.
func TestAFailedCutoverReportsItsReasonAndWhereItStopped(t *testing.T) {
	var observed []observability.Observation
	repository := &RedisCatalogRepository{}
	repository.ConfigureObserver(observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			observed = append(observed, observation)
		}))
	facts := newCutoverFacts()
	facts.failedAt("qg-that-stopped-it")

	repository.observeCutover(context.Background(), facts,
		fmt.Errorf("%w: a kept Query Group has a Plan without a current activation record", ErrActivationRecordMissing))

	if len(observed) != 1 || observed[0].ScheduleCutover == nil {
		t.Fatalf("observed = %+v, want one cutover observation", observed)
	}
	reported := observed[0].ScheduleCutover
	if reported.Result != "failure" {
		t.Fatalf("result = %q, want failure", reported.Result)
	}
	if reported.Reason != CutoverReasonActivationRecordMissing {
		t.Fatalf("reason = %q, want %q", reported.Reason, CutoverReasonActivationRecordMissing)
	}
	if reported.QueryGroup != "qg-that-stopped-it" {
		t.Fatalf("query group = %q, want the one the cutover stopped on", reported.QueryGroup)
	}
	if observed[0].Err == nil {
		t.Fatal("the observation carries no error; the reason is bounded on purpose and the error is " +
			"what says which of that reason's several causes it was")
	}
}

// A cutover that worked reports no reason, because there is nothing to explain.
func TestASuccessfulCutoverReportsNoReason(t *testing.T) {
	var observed []observability.Observation
	repository := &RedisCatalogRepository{}
	repository.ConfigureObserver(observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			observed = append(observed, observation)
		}))
	facts := newCutoverFacts()
	facts.started = time.Now()

	repository.observeCutover(context.Background(), facts, nil)

	if len(observed) != 1 || observed[0].ScheduleCutover == nil {
		t.Fatalf("observed = %+v, want one cutover observation", observed)
	}
	if reported := observed[0].ScheduleCutover; reported.Result != "success" || reported.Reason != "" {
		t.Fatalf("result = %q reason = %q, want success with no reason", reported.Result, reported.Reason)
	}
}

// A Segment cannot be cut for a Query Group the publication does not name.
//
// Refusing is the point. Deriving a name from the group instead is exactly the
// second derivation this change removed, and the damage it does is invisible
// until a cutover refuses the mismatch weeks later -- by which time the fleet
// has been executing content nobody published for as long as that took.
func TestASegmentIsNotCutForAQueryGroupThePublicationDoesNotName(t *testing.T) {
	_, err := scheduleSegmentForGroup(
		SnapshotPublicationRef{SnapshotRevision: "rev", PublicationEpoch: 1},
		QueryGroup{Identity: "qg-a"}, 60, ContentEntry{},
	)
	if err == nil {
		t.Fatal("a Segment was cut with no name from the publication")
	}
	if cutoverFailureReason(err) != CutoverReasonInvalidRequest {
		t.Fatalf("reason = %q, want %q", cutoverFailureReason(err), CutoverReasonInvalidRequest)
	}
}

// A Segment carries the names it was given, unchanged.
func TestASegmentCarriesTheNamesItWasGiven(t *testing.T) {
	group := QueryGroup{Identity: "qg-a", Plans: []FrozenPlan{{
		Identity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "1"},
		Plan:     contract.EvaluationPlanV2{PlanID: "1"},
	}}}
	named := namedContentForTest(t, group)

	segment, err := scheduleSegmentForGroup(
		SnapshotPublicationRef{SnapshotRevision: "rev", PublicationEpoch: 1}, group, 60, named)
	if err != nil {
		t.Fatal(err)
	}
	if segment.ObjectDigest != named.Digest {
		t.Fatalf("segment names %q, want the object the publication named (%q)",
			segment.ObjectDigest, named.Digest)
	}
	if len(segment.OutputContextRefs) != 1 || segment.OutputContextRefs[0].Digest != named.Refs[0].Digest {
		t.Fatalf("segment output contexts = %+v, want the ones the publication named", segment.OutputContextRefs)
	}
}

// A Segment is refused when the content does not hash to the name it is being
// given.
//
// Copying the name stops assembly from changing a name. It does not stop
// assembly from changing the content, and a Segment naming one object while
// carrying another is the same fault wearing the other mask -- it would be
// written, it would verify against its own digest, and it would be found weeks
// later by a cutover refusing a mismatch nobody could explain.
func TestASegmentIsRefusedWhenItsContentDoesNotMatchItsName(t *testing.T) {
	group := QueryGroup{Identity: "qg-a", Plans: []FrozenPlan{{
		Identity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "1"},
		Plan:     contract.EvaluationPlanV2{PlanID: "1", NoData: &contract.NoDataConfigV1{Continuous: 1, Level: 2}},
	}}}
	// The manifest names the content as published; the group comes back from
	// assembly with the section gone, which is exactly what happened.
	named := namedContentForTest(t, group)
	lossy := group
	lossy.Plans = append([]FrozenPlan(nil), group.Plans...)
	lossy.Plans[0].Plan.NoData = nil

	_, err := scheduleSegmentForGroup(
		SnapshotPublicationRef{SnapshotRevision: "rev", PublicationEpoch: 1}, lossy, 60, named)

	if err == nil {
		t.Fatal("a Segment was cut naming an object its content does not hash to")
	}
	if got := cutoverFailureReason(err); got != CutoverReasonSegmentContentMismatch {
		t.Fatalf("reason = %q, want %q", got, CutoverReasonSegmentContentMismatch)
	}
	var conflict *ScheduleConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("the refusal carries no conflict: %v", err)
	}
	if !strings.Contains(conflict.Detail, "manifest_digest=") ||
		!strings.Contains(conflict.Detail, "assembled_digest=") {
		t.Fatalf("detail = %q, want both digests", conflict.Detail)
	}
}

// The output context names are checked too, not only the object.
func TestASegmentIsRefusedWhenAnOutputContextDoesNotMatch(t *testing.T) {
	group := QueryGroup{Identity: "qg-a", Plans: []FrozenPlan{{
		Identity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "1"},
		Plan:     contract.EvaluationPlanV2{PlanID: "1"},
	}}}
	named := namedContentForTest(t, group)
	named.Refs = []execution.OutputContextRef{{
		Plan: group.Plans[0].Identity, Digest: execution.OutputContextDigest("something-else"),
	}}

	_, err := scheduleSegmentForGroup(
		SnapshotPublicationRef{SnapshotRevision: "rev", PublicationEpoch: 1}, group, 60, named)

	if got := cutoverFailureReason(err); got != CutoverReasonSegmentContentMismatch {
		t.Fatalf("reason = %q, want %q; err = %v", got, CutoverReasonSegmentContentMismatch, err)
	}
}

// namedContentForTest is the manifest entry a publication of this group writes.
func namedContentForTest(t *testing.T, group QueryGroup) ContentEntry {
	t.Helper()
	digest, err := DeriveQueryGroupObjectDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	refs := make([]execution.OutputContextRef, 0, len(group.Plans))
	for _, plan := range group.Plans {
		contextDigest, err := DeriveOutputContextDigest(plan)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, execution.OutputContextRef{Plan: plan.Identity, Digest: contextDigest})
	}
	return ContentEntry{Digest: digest, Refs: refs}
}
