// Tencent is pleased to support the open source community by making
// BlueKing available. Copyright (C) 2017-2025 Tencent. Licensed under the MIT License.

package controlplane

import (
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ErrObservationAmbiguous is a strategy whose rows name two identities or
// two current rows; ErrObservationChanged a read pinned to a directory
// revision that is no longer the current one.
var ErrObservationAmbiguous = errors.New("observation identity is ambiguous")
var ErrObservationChanged = errors.New("observation revision changed")

type StrategyDirectoryRow struct {
	QueryRevision    execution.QueryRevision       `json:"query_revision"`
	ScheduleRevision execution.ScheduleRevision    `json:"schedule_revision"`
	Identity         execution.PlanIdentity        `json:"identity"`
	QueryGroup       execution.QueryGroupIdentity  `json:"query_group"`
	ObjectDigest     execution.ObjectDigest        `json:"object_digest"`
	Publication      SnapshotPublicationRef        `json:"publication"`
	Role             string                        `json:"role"`
	Activation       *execution.PlanActivationFact `json:"activation,omitempty"`
	// ActivatedOn is the publication the row's activation record sits on,
	// when it is not the one the row's content is from: a Plan whose Segment
	// opened on an earlier publication keeps its record there while its
	// content follows every cutover.
	ActivatedOn *SnapshotPublicationRef `json:"activated_on,omitempty"`
	// OutputContext names the output context this Plan renders by, which is
	// where its frozen wire format lives: the execution object above is
	// deliberately without it. Empty when the publication's manifest was not
	// in hand at refresh and the runtime had not remembered the content
	// either, which the output read reports as unknown rather than fetching a
	// manifest to find out.
	OutputContext execution.OutputContextDigest `json:"output_context_digest,omitempty"`
	// ContentNotHeld is a Plan the activation carries whose content the
	// Leader does not hold: a draining Query Group whose content the
	// activation round has not read, or a Plan of the current publication
	// while a later one is published and the current one's content is not in
	// memory.
	// The row names the Plan, its publication and its activation, and
	// nothing the content would have said - the Query Group, the object, the
	// revisions - which is left empty rather than guessed.
	ContentNotHeld bool `json:"content_not_held,omitempty"`
}

// DirectoryPublication is one publication a directory answer names: how many
// active Plans the activation carries on it, and whether its content is in
// memory - the publication the Leader made, and one the activation round
// holds - or not_held. A Plan's content is the current publication's unless
// the Plan drains (it has records on two publications), whatever
// publication its record sits on; a Plan whose content is not held is named
// from the activation alone (ContentNotHeld).
type DirectoryPublication struct {
	Publication SnapshotPublicationRef `json:"publication"`
	Plans       int                    `json:"plans"`
	Manifest    string                 `json:"manifest,omitempty"`
}

type StrategyDirectorySnapshot struct {
	ObservedAt time.Time `json:"observed_at"`
	Revision   string    `json:"revision"`
	Complete   bool      `json:"complete"`
	Reason     string    `json:"reason,omitempty"`
	// FailedRead and Error are the first read that failed the answer and
	// its own words, bounded and with URLs redacted: activation, manifest
	// or group_object. The reason alone said DEPENDENCY_UNAVAILABLE
	// for a manifest read that failed on one replica every refresh, and the
	// cost ranking fed from this directory tracked nothing, with no way to
	// tell a read timeout from a missing key or a body that did not decode.
	FailedRead         string                 `json:"failed_read,omitempty"`
	Error              string                 `json:"error,omitempty"`
	Published          SnapshotPublicationRef `json:"published"`
	Current            SnapshotPublicationRef `json:"current"`
	ActivationRevision uint64                 `json:"activation_revision"`
	SourceObservation  string                 `json:"source_observation"`
	SourceComplete     bool                   `json:"source_complete"`
	SourceReason       string                 `json:"source_reason,omitempty"`
	SourceMatchedTotal int                    `json:"source_matched_total"`
	SourceTruncated    bool                   `json:"source_truncated"`
	// FailedKey and FailedPublication are what that step was reading: the
	// store key, and the publication whose manifest or object it was. The
	// step and its words said a manifest was missing on every replica every
	// refresh, and not whose -- the latest publication's or one an active
	// Plan was still carried on -- which is the whole question.
	FailedKey         string                  `json:"failed_key,omitempty"`
	FailedPublication *SnapshotPublicationRef `json:"failed_publication,omitempty"`
	// Publications is every publication the refresh read a manifest for:
	// the latest, then each one an active Plan's activation still names,
	// with how many active Plans name it and where its manifest came from.
	Publications []DirectoryPublication `json:"publications,omitempty"`
	// Source dispositions have no tenant identity in the persisted contract.
	// They remain unattributed, never joined to a similarly named tenant Plan.
	Unattributed []ObjectDisposition    `json:"source_unattributed,omitempty"`
	Rows         []StrategyDirectoryRow `json:"rows"`
	GroupsKnown  int                    `json:"groups_known"`
	GroupsTotal  int                    `json:"groups_total"`
}

// rememberedContextRefs is a publication's Plan -> output context naming out
// of the content this process read last, when that content is the
// publication asked for. No read: the
// memo is what an activation round already paid for.
func (repository *RedisCatalogRepository) rememberedContextRefs(publication SnapshotPublicationRef) map[execution.PlanIdentity]execution.OutputContextDigest {
	if repository == nil {
		return nil
	}
	content, ok := repository.contentMemo.lookup(publication)
	if !ok {
		return nil
	}
	contexts := make(map[execution.PlanIdentity]execution.OutputContextDigest)
	for _, group := range content.Groups {
		for _, ref := range group.Refs {
			contexts[ref.Plan] = ref.Digest
		}
	}
	return contexts
}

// Why an output read could not say what a Plan publishes as. Closed: a
// reader shows these words and no others.
const (
	// OutputContextRefNotRetained: the directory row carries no output
	// context digest -- see StrategyDirectoryRow.OutputContext.
	OutputContextRefNotRetained = "OUTPUT_CONTEXT_REF_NOT_RETAINED"
	// OutputContextUnavailable: the object the row names is not in the store.
	OutputContextUnavailable = "OUTPUT_CONTEXT_UNAVAILABLE"
	// OutputContextCorrupt: bytes were there and did not hash to the digest
	// that names them, or did not decode as an output context.
	OutputContextCorrupt = "OUTPUT_CONTEXT_CORRUPT"
	// OutputContextDependency: the store did not answer.
	OutputContextDependency = "DEPENDENCY_UNAVAILABLE"
)

// OutputFormatReasons is every reason OutputFormatFacts can carry.
var OutputFormatReasons = []string{OutputContextRefNotRetained, OutputContextUnavailable, OutputContextCorrupt, OutputContextDependency}

// OutputFormatFacts is what one Plan's events are published as, read off the
// output context the control leader froze with the Plan -- not off the
// deployment's current choice, which is a different fact. It answers the
// question a forced choice leaves open and an automatic one leaves silent:
// for this strategy, which format did the choice come out as.
type OutputFormatFacts struct {
	// Known is whether the object was read. False comes with a Reason and
	// nothing else; the fields below are then not zero values of a fact but
	// the absence of one.
	Known  bool   `json:"known"`
	Reason string `json:"reason,omitempty"`
	// OutputContextDigest names the object read, so a reader can tell two
	// answers about one strategy apart when the Plan was rebuilt between them.
	OutputContextDigest execution.OutputContextDigest `json:"output_context_digest,omitempty"`
	// WireFormat is the word frozen in the object. Empty on an object written
	// before the choice existed, where the revision was the whole rule.
	WireFormat string `json:"wire_format,omitempty"`
	// EffectiveWireFormat is the format the sink writes for this Plan, and
	// DecidedBy how that is known, one of WireFormatDecisions: FROZEN when
	// the word above is there and is what is written, REVISION_RULE when the
	// rule the readers apply to an object without one decided it, and
	// HISTORICAL_WORD_RESOLVED when the word above is from an earlier rule
	// and the readers resolve it to a current format -- the two fields then
	// differ on purpose, and both are the truth.
	EffectiveWireFormat string `json:"effective_wire_format,omitempty"`
	DecidedBy           string `json:"decided_by,omitempty"`
	// SnapshotRevision is the frozen strategy revision the rule reads. Zero
	// is the input that sends a strategy the compatible way under auto.
	SnapshotRevision int64 `json:"snapshot_revision"`
	// SignalType is what the events are observed from, frozen beside the
	// format; empty when the build could not name it.
	SignalType string `json:"signal_type,omitempty"`
	// CompatibilityContext is whether the object carries the context the
	// Python-compatible conversion reads. It is attached when the Plan
	// publishes that protocol, so under a forced legacy choice a strategy with
	// a revision has it and under native none does.
	CompatibilityContext bool `json:"compatibility_context"`
}
