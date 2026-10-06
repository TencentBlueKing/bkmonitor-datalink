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
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Why a publication cutover failed.
//
// The cutover is what moves the fleet onto newly published execution content:
// it closes the open Segment of every Query Group whose content changed and
// opens a new one naming the new object. A cutover that fails leaves every
// Segment where it is, so the fleet keeps executing what it was executing, and
// every later publication fails the same way at the same place.
//
// Until now it reported only that it failed. For eleven hours it failed about
// twice a minute with no reason attached, and the only dimension it moved was
// a result label on a duration histogram; what a reader saw of it was a fleet
// whose configuration changes had simply stopped taking effect. These names
// exist so the next one says what happened.
const (
	// CutoverReasonActivationRecordMissing is the activation and the open
	// Segments disagreeing about which Plans exist: a Plan the cutover would
	// keep has no current activation record, or the activation holds a record
	// no open Segment carries. Neither can be recovered from the other, so the
	// cutover refuses rather than drop an activation.
	CutoverReasonActivationRecordMissing = "activation_record_missing"
	// The four ways an open Segment can fail the cutover's preconditions. They
	// were one reason until the one that fires in production had to be named:
	// all four are "a Segment is not in the state the cutover requires", and
	// which one it is decides whether the fix is to recover, to wait, or to
	// stop comparing.
	//
	// CutoverReasonTimelineMissing is a Query Group whose timeline holds no
	// Segment at all, or has been retired out from under the cutover.
	CutoverReasonTimelineMissing = "timeline_missing_or_retired"
	// CutoverReasonOpenSegmentClosed is an open Segment that is already closed,
	// or that starts at or after the boundary this cutover is cutting at. A
	// cutover that stopped half way leaves Segments in exactly this state.
	CutoverReasonOpenSegmentClosed = "open_segment_closed_or_ahead"
	// CutoverReasonOpenDigestMismatch is the open Segment naming content that
	// is not what the last activation says it should name. The Segment was
	// moved by something other than this path -- including a previous cutover
	// of this path that wrote Segments and did not land its activation.
	CutoverReasonOpenDigestMismatch = "open_digest_mismatch"
	// CutoverReasonLegacyRevisionMismatch is a Segment from before Segments
	// named their content, whose query or schedule revision disagrees with the
	// group it is being compared against.
	CutoverReasonLegacyRevisionMismatch = "legacy_revision_mismatch"
	// CutoverReasonSegmentContentMismatch is the publication's own names
	// disagreeing with the content it hands the cutter: the manifest says this
	// Query Group's object is one digest and the group that came back from the
	// object store hashes to another.
	//
	// It means assembly changed the content on the way back -- a field dropped,
	// a field added, an encoding that no longer round-trips. That has happened,
	// it named every Segment cut during it after an object the manifest did not
	// name, and it was invisible for eleven hours because nothing compared the
	// two. Comparing them makes it a refusal at the moment it happens, on the
	// Query Group it happens to, with both digests in the line.
	CutoverReasonSegmentContentMismatch = "segment_content_mismatch"
	// CutoverReasonSegmentConflict is a Segment precondition that none of the
	// four above names. Like other, it should stay at zero.
	CutoverReasonSegmentConflict = "segment_conflict"
	// CutoverReasonDigestMismatch is a stored object that does not hash to the
	// digest it is named by, or is not the object that digest should name.
	CutoverReasonDigestMismatch = "digest_mismatch"
	// CutoverReasonObjectNewer is a stored object of a contract version this
	// build does not read yet: the leader that published it is newer than
	// this replica. It lasts as long as the rollout and clears by itself
	// when this replica is replaced; reading it as a digest mismatch made a
	// version bump look like every object breaking at once.
	CutoverReasonObjectNewer = "object_newer"
	// CutoverReasonConflict is the compare-and-set losing: another writer moved
	// the activation or a timeline first. This one is expected occasionally and
	// resolves by itself on the next round.
	CutoverReasonConflict = "conflict"
	// CutoverReasonUnavailable is content the cutover needs and cannot read,
	// because it is not stored or has expired.
	CutoverReasonUnavailable = "unavailable"
	// CutoverReasonInvalidRequest is the cutover being asked for something that
	// is not a cutover: no activation to advance from, more than one new
	// publication, an invalid draining projection.
	CutoverReasonInvalidRequest = "invalid_request"
	// CutoverReasonIO is the store failing underneath.
	CutoverReasonIO = "io"
	// CutoverReasonOther is a failure none of the above names.
	//
	// It is not a catch-all with a job. Every failure this function can return
	// is mapped above, so a non-zero other means a failure path was added and
	// not named -- which is the state this whole family was created out of.
	CutoverReasonOther = "other"
)

// CutoverReasons is every reason, for the metric to create each label at
// startup and for a reader to bound the family by.
var CutoverReasons = []string{
	CutoverReasonActivationRecordMissing,
	CutoverReasonTimelineMissing, CutoverReasonOpenSegmentClosed,
	CutoverReasonOpenDigestMismatch, CutoverReasonLegacyRevisionMismatch,
	CutoverReasonSegmentContentMismatch, CutoverReasonSegmentConflict,
	CutoverReasonDigestMismatch, CutoverReasonObjectNewer, CutoverReasonConflict, CutoverReasonUnavailable,
	CutoverReasonInvalidRequest, CutoverReasonIO, CutoverReasonOther,
}

// ScheduleConflictError is one of the cutover's Segment preconditions failing,
// with which one and the values it compared.
//
// It wraps ErrScheduleConflict so every existing reader of that sentinel keeps
// working. What it adds is the two things a reader needs and did not have: the
// name of the precondition, and the values on either side of the comparison
// that failed. "A Segment is not in the state the cutover requires" is not
// something anyone can act on; "this Segment names digest A and the activation
// says B" is.
type ScheduleConflictError struct {
	Reason     string
	QueryGroup execution.QueryGroupIdentity
	// Detail renders the comparison that failed. Bounded text for a log line,
	// never a metric label.
	Detail string
}

func (e *ScheduleConflictError) Error() string {
	return fmt.Sprintf("alarmd controlplane: schedule activation conflict (%s) on %s: %s",
		e.Reason, e.QueryGroup, e.Detail)
}

func (e *ScheduleConflictError) Unwrap() error { return ErrScheduleConflict }

// scheduleConflict builds one, so the four sites read as what they check.
func scheduleConflict(reason string, group execution.QueryGroupIdentity, detail string) error {
	return &ScheduleConflictError{Reason: reason, QueryGroup: group, Detail: detail}
}

// ErrActivationRecordMissing is the activation and the open Segments
// disagreeing about which Plans exist. A sentinel rather than a bare string so
// the failure can be named where it is reported, which is not where it is
// raised.
var ErrActivationRecordMissing = errors.New("alarmd controlplane: activation records and open Segments disagree")

// ErrCutoverRequest is the cutover being asked for something that is not a
// cutover.
var ErrCutoverRequest = errors.New("alarmd controlplane: invalid publication schedule activation request")

// cutoverFailureReason names a cutover failure.
//
// Ordered most specific first: a wrapped error can satisfy more than one of
// these, and the first match is the one that says the most. Nothing reaches
// other unless a new failure path was added without a name.
func cutoverFailureReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrActivationRecordMissing):
		return CutoverReasonActivationRecordMissing
	case errors.Is(err, ErrCutoverRequest):
		return CutoverReasonInvalidRequest
	case errors.Is(err, ErrScheduleConflict):
		var conflict *ScheduleConflictError
		if errors.As(err, &conflict) && conflict.Reason != "" {
			return conflict.Reason
		}
		return CutoverReasonSegmentConflict
	case errors.Is(err, ErrActivationConflict):
		return CutoverReasonConflict
	case errors.Is(err, ErrCatalogObjectContractNewer):
		return CutoverReasonObjectNewer
	case errors.Is(err, ErrCatalogObjectCorrupt):
		return CutoverReasonDigestMismatch
	case errors.Is(err, ErrCatalogObjectUnavailable),
		errors.Is(err, ErrCatalogManifestUnavailable),
		errors.Is(err, ErrSnapshotUnavailable),
		errors.Is(err, ErrActivationUnavailable):
		return CutoverReasonUnavailable
	}
	var corrupt *PersistedSnapshotCorruptError
	if errors.As(err, &corrupt) {
		return CutoverReasonDigestMismatch
	}
	var dependency *ActivationDependencyIOError
	if errors.As(err, &dependency) {
		return CutoverReasonIO
	}
	return CutoverReasonOther
}

// failedAt records the Query Group the cutover is working on, so a failure
// names one. The cutover returns at the first failure, so whatever it was
// working on when it stopped is the one that stopped it.
func (facts *cutoverFacts) failedAt(group execution.QueryGroupIdentity) {
	if facts == nil {
		return
	}
	facts.group = string(group)
}

// verifySegmentContent checks that the group a Segment is cut from is the
// content the publication named.
//
// The names are copied from the manifest and the content comes back from the
// object store, so the two have different provenances and only this comparison
// ties them together. It is deliberately the derivation the manifest itself
// used: an agreement here means the bytes that were published are the bytes
// that came back, and a disagreement names both sides.
func verifySegmentContent(group QueryGroup, named ContentEntry) error {
	assembled, err := DeriveQueryGroupObjectDigest(group)
	if err != nil {
		return err
	}
	if assembled != named.Digest {
		return scheduleConflict(CutoverReasonSegmentContentMismatch, group.Identity, fmt.Sprintf(
			"manifest_digest=%s assembled_digest=%s", named.Digest, assembled))
	}
	byPlan := make(map[execution.PlanIdentity]execution.OutputContextDigest, len(named.Refs))
	for _, ref := range named.Refs {
		byPlan[ref.Plan] = ref.Digest
	}
	for _, plan := range group.Plans {
		digest, err := DeriveOutputContextDigest(plan)
		if err != nil {
			return err
		}
		wanted, ok := byPlan[plan.Identity]
		if !ok {
			return scheduleConflict(CutoverReasonSegmentContentMismatch, group.Identity, fmt.Sprintf(
				"manifest_refs name no output context for Plan %s assembled_ref=%s",
				plan.Identity.StrategyID, digest))
		}
		if wanted != digest {
			return scheduleConflict(CutoverReasonSegmentContentMismatch, group.Identity, fmt.Sprintf(
				"plan=%s manifest_ref=%s assembled_ref=%s", plan.Identity.StrategyID, wanted, digest))
		}
	}
	if len(named.Refs) != len(group.Plans) {
		return scheduleConflict(CutoverReasonSegmentContentMismatch, group.Identity, fmt.Sprintf(
			"manifest_refs=%d assembled_plans=%d", len(named.Refs), len(group.Plans)))
	}
	return nil
}
