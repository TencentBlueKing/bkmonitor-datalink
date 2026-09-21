// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type NoDataLoadStatus string

const (
	// NoDataMemoryMissing is a Plan with no stored memory. It is the normal
	// state before a Plan's first no-data round and it loads as empty memory.
	NoDataMemoryMissing NoDataLoadStatus = "MISSING"
	// NoDataMemoryFound is a stored memory this build can read. Zero groups is a
	// legitimate found record: the Plan remembers nothing, which is different
	// from never having written one because the two carry different marker
	// revisions.
	NoDataMemoryFound NoDataLoadStatus = "FOUND"
	// NoDataMemoryUnreadable is a record written in a schema newer than this
	// build understands, which happens on a rollback.
	//
	// It is a status rather than an error on purpose. During a rollback it is
	// every no-data Plan, every round, for as long as the rollback lasts, and an
	// error would fail the Plan's whole evaluation - taking its threshold
	// detection down with it over a no-data record nobody was asking about. So
	// the Plan is evaluated as usual and only its no-data detection pauses: the
	// record is not loaded, not applied to and not cleared, and the Slot says so
	// by name.
	//
	// The payload is refused with it. A reader that took the groups from a
	// record whose shape it does not know would be guessing at fields it has no
	// definition for, and the fields it silently dropped would be exactly the
	// ones the newer schema added.
	NoDataMemoryUnreadable NoDataLoadStatus = "UNREADABLE"
	// NoDataMemoryUnavailable is a retryable failure to read the record.
	NoDataMemoryUnavailable NoDataLoadStatus = "UNAVAILABLE"
	// NoDataMemoryTerminal is a deterministic failure to read the record.
	NoDataMemoryTerminal NoDataLoadStatus = "TERMINAL"
)

type NoDataLoadRequest struct {
	Contract FrozenExecutionContractRef
	Items    []PlanNoDataLoadItem
}

type NoDataApplyStatus string

const (
	NoDataApplied        NoDataApplyStatus = "APPLIED"
	NoDataAlreadyApplied NoDataApplyStatus = "ALREADY_APPLIED"
	NoDataStale          NoDataApplyStatus = "STALE_VERSION"
	NoDataConflict       NoDataApplyStatus = "CONFLICT"
	NoDataRetryable      NoDataApplyStatus = "RETRYABLE_IO"
	NoDataRejected       NoDataApplyStatus = "DETERMINISTIC_INVALID"
)

type NoDataApplyRequest struct {
	Contract FrozenExecutionContractRef
	Items    []PlanNoDataMutation
}

type NoDataApplyItemResult struct {
	Identity   PlanNoDataIdentity
	Status     NoDataApplyStatus
	ReasonCode ReasonCode
	// Size is set only by a refusal about how large the record is, and carries
	// the measurement that refusal made. STATE_BUDGET_EXCEEDED on its own says
	// a Plan's memory did not fit and leaves a reader with no way to tell a
	// record a little over the bound from one many times it, or to tell which
	// of the two records was measured -- and those are different situations
	// with different remedies.
	Size *NoDataRecordSize
	// Conflict is set only by CONFLICT, and says which comparison refused.
	// Without it every conflict reads the same, and two of them are not: a
	// revision that moved is somebody else's write landing first, while one
	// statement meeting another of its own version is two writers describing
	// the same round differently, which no retry resolves on its own.
	Conflict *NoDataConflictFacts
}

// NoDataConflictFacts names the comparison that refused a write and the two
// statements it compared.
type NoDataConflictFacts struct {
	// Kind is the store-wide conflict vocabulary, not a second one. The same
	// phenomenon in the runtime state store already has these names, and a
	// no-data conflict that spelled them differently would make one question
	// need two queries.
	Kind StateVersionConflictKind
	// Persisted and Proposed are the memory digests either side of the
	// comparison, set when the conflict is about what the two say rather than
	// about which came first. A conflict that names no values is a conflict
	// nobody can act on.
	Persisted MutationDigest
	Proposed  MutationDigest
	// ExpectedRevision is the revision the statement was derived against and
	// StoredRevision the one the record holds. They are carried on every
	// conflict, not only the ones about revisions: a reader looking at a wall
	// of refusals needs to see whether the two are far apart, equal, or one of
	// them zero, and that answer separates a race between writers from a
	// statement derived against another record entirely.
	ExpectedRevision uint64
	StoredRevision   uint64
	// DerivedFrom is the record the statement was built from, which is the one
	// fact that tells a rollout apart from a race.
	DerivedFrom NoDataRepresentation
}

// NoDataRepresentation names which stored shape a memory was read from.
type NoDataRepresentation string

const (
	// NoDataRepresentationNone is a memory that was not read: missing, or a
	// read that failed. It is a value rather than an empty string so the
	// partition adds up -- every load lands on exactly one of these, and a
	// reader can check that against the number of Plans that were asked for.
	NoDataRepresentationNone NoDataRepresentation = "NONE"
	// NoDataRepresentationWholeMemory is the single-value record this build
	// reads and no longer writes. A Plan on it has not written since the
	// upgrade, or has been back to an older build since.
	NoDataRepresentationWholeMemory NoDataRepresentation = "WHOLE_MEMORY"
	// NoDataRepresentationPerGroup is the record this build writes.
	NoDataRepresentationPerGroup NoDataRepresentation = "PER_GROUP"
)

// NoDataRepresentations is every value the label may take, for the metric to
// pre-create and bound itself by. The zero reading is the one that matters
// here: WHOLE_MEMORY falling to zero is what says the fleet has finished
// rolling, and a series that is absent rather than zero cannot say that.
var NoDataRepresentations = []NoDataRepresentation{
	NoDataRepresentationNone, NoDataRepresentationWholeMemory, NoDataRepresentationPerGroup,
}

// NoDataRecordKind names which record a size refusal measured.
type NoDataRecordKind string

const (
	// NoDataRecordGroups is how many groups the memory this round would store
	// holds, against the bound on that number. It is the only measurement an
	// apply refusal carries.
	//
	// It replaced a byte bound rather than joining one. A byte bound belonged
	// to a memory held in a single value, and it was reached by an ordinary
	// Plan with enough groups - silently, at the moment absence was being
	// detected, which is when each group's entry grows. One field per group has
	// no such bound, so nothing this build writes can be refused for its size,
	// and the shape that used to report it is gone rather than kept as a label
	// nothing can produce.
	NoDataRecordGroups NoDataRecordKind = "GROUPS"
)

// NoDataRecordSize is the measurement behind a bound refusal: what was
// measured, the measurement, and the bound it was taken against.
type NoDataRecordSize struct {
	Record NoDataRecordKind
	Groups int
	Limit  int
}

// NoDataRefusal is one shape a deterministic refusal takes, for the partition
// to pre-create and for a reader to bound the family by.
type NoDataRefusal struct {
	Reason ReasonCode
	Record NoDataRecordKind
}

// NoDataRefusals is every shape a deterministic no-data apply refusal takes.
//
// It is a closed list because every refusal in the store names its reason in
// that file, including the one that passes a stored record's reason through:
// the decoder produces exactly one terminal reason. That is what makes this
// safe to publish and to bound a metric label by, and the store's own test
// drives each condition and checks the pair it produced is here -- a list kept
// by hand beside code that can produce anything is the shape that reads as a
// bound while not being one.
var NoDataRefusals = []NoDataRefusal{
	{Reason: ReasonCode(contract.ReasonStateBudgetExceeded), Record: NoDataRecordGroups},
	{Reason: ReasonCode(contract.ReasonStateCorrupt)},
	{Reason: ReasonCode(contract.ReasonBackendCapabilityMissing)},
	{Reason: ReasonCode(contract.ReasonStateSchemaUnsupported)},
}

// NoDataWriteOutcomes is every outcome of a no-data memory write that is not a
// deterministic refusal.
//
// The refusals are reported on their own, with the store's reason and the
// measurement behind it; these are the rest, and they have to be reported too.
// A Plan whose write keeps landing on CONFLICT stores nothing, round after
// round, exactly as a refused one does -- and with only the refusals reported,
// that Plan is silent. The two lists together are every mutation the store was
// asked for, which is what makes "is this Plan's memory being kept" answerable
// rather than inferable from an absence of complaints.
var NoDataWriteOutcomes = noDataWriteOutcomes()

// NoDataApplyStatuses is every status an apply can return. The two lists that
// partition it are derived from this one rather than written beside it: two
// hand-kept lists whose union has to be this one is the shape where a status
// added later goes missing from both and nothing notices.
var NoDataApplyStatuses = []NoDataApplyStatus{
	NoDataApplied, NoDataAlreadyApplied, NoDataStale, NoDataConflict, NoDataRetryable, NoDataRejected,
}

func noDataWriteOutcomes() []NoDataApplyStatus {
	outcomes := make([]NoDataApplyStatus, 0, len(NoDataApplyStatuses)-1)
	for _, status := range NoDataApplyStatuses {
		if status == NoDataRejected {
			continue
		}
		outcomes = append(outcomes, status)
	}
	return outcomes
}

// NoDataWriteStored reports whether an outcome means the store now holds what
// the round wanted written.
//
// ALREADY_APPLIED counts: the record carries this round's own version and
// digest, so the memory the next round reads is the one this round produced.
// STALE_VERSION does not, and it is the one that reads like success and is not
// -- a newer record won, and what this round learned was dropped.
func NoDataWriteStored(status NoDataApplyStatus) bool {
	return status == NoDataApplied || status == NoDataAlreadyApplied
}

type NoDataApplyResult struct {
	Items []NoDataApplyItemResult
}

// NoDataMemorySnapshot is one Plan's stored no-data memory as it was read.
type NoDataMemorySnapshot struct {
	Identity                PlanNoDataIdentity
	MarkerRevision          uint64
	PersistedApplyVersion   ApplyVersion
	PersistedMutationDigest MutationDigest
	Status                  NoDataLoadStatus
	// SchemaVersion is the shape the record was written in. It is carried even
	// when the record is unreadable, and especially then: it is the only thing
	// that says which build wrote it.
	SchemaVersion        NoDataMemorySchema
	LastScheduleRevision PlanScheduleRevision
	RosterVersion        string
	// Representation says which of the two stored shapes this snapshot was read
	// from, and travels with the snapshot rather than being worked out again by
	// a reader: the choice is made once, inside the store, from two records
	// only it saw.
	//
	// It is the one reading that says how far the rollout has got. A fleet
	// still on the whole-memory record and one fully moved over look identical
	// from every other signal -- the memory is read, the Plan evaluates, the
	// write goes through -- and the difference is exactly what decides whether
	// the cleanup may run.
	Representation NoDataRepresentation
	// PresentAsOf is the round the record says the Plan last had data in.
	//
	// A v1 record does not hold the field, and reading it as zero was wrong. It
	// is not a record of a Plan that has never had data -- every group in it
	// carries a last-seen time -- and zero is the one value that contradicts
	// them all: the memory invariant is that no group was last seen after the
	// Plan last had data, so a v1 record with any group in it failed to derive
	// a mutation at all, on every round where nothing reported. It is derived
	// instead, as the newest last-seen time the record holds, which is what the
	// field means and what the v2 record would have stored.
	PresentAsOf int64
	Groups      []NoDataGroupMemory
	ReasonCode  ReasonCode
}

// NoDataMemoryRenewal is one renewal that actually reached the store.
//
// It is reported beside the snapshots rather than on one, because it is not a
// fact about the record: it is what was done to the key, and it is done for a
// record this build cannot read just as much as for one it can -- a paused
// Plan's memory must not expire while the rollback it is waiting out is still
// going on.
//
// Renewal is the only thing keeping a steady Plan's memory alive under the
// per-group representation: such a Plan writes nothing at all for as long as
// its groups do not change, so a renewal that quietly stopped working would
// expire every one of those memories, and the first anybody would hear of it is
// every group of every quiet Plan starting again with no history.
type NoDataMemoryRenewal struct {
	Identity PlanNoDataIdentity
	// Renewed is what the store answered: false means the key had enough life
	// left, which is the ordinary case and not a failure.
	Renewed    bool
	TTLSeconds int64
	// ReasonCode is empty on success. A renewal that failed names why, because
	// "renewals are not happening" and "renewals are happening and the backend
	// cannot do them" send a reader to different places.
	ReasonCode ReasonCode
}

type NoDataLoadResult struct {
	Items []NoDataMemorySnapshot
	// Renewals holds one entry per load that reached the store, which is far
	// fewer than the loads: the gate answers most of them from what this
	// process already knows.
	Renewals []NoDataMemoryRenewal
}

func (result NoDataLoadResult) Find(identity PlanNoDataIdentity) (NoDataMemorySnapshot, bool) {
	for _, item := range result.Items {
		if item.Identity == identity {
			return item, true
		}
	}
	return NoDataMemorySnapshot{}, false
}

func noDataSnapshotHasPayload(snapshot NoDataMemorySnapshot) bool {
	// The representation is not payload when it says NONE. It is the answer to
	// "which record did this come from" for a snapshot that came from no
	// record, and every snapshot carries it: the writer has to know which
	// record a statement was derived from, and a field left empty for the
	// no-record case gives that reader two spellings for one answer.
	namesARecord := snapshot.Representation != "" && snapshot.Representation != NoDataRepresentationNone
	return snapshot.MarkerRevision != 0 || snapshot.PersistedMutationDigest != "" ||
		snapshot.PersistedApplyVersion != (ApplyVersion{}) || snapshot.LastScheduleRevision != "" ||
		snapshot.RosterVersion != "" || snapshot.PresentAsOf != 0 || namesARecord ||
		len(snapshot.Groups) != 0
}

func ValidateNoDataLoad(request NoDataLoadRequest, result NoDataLoadResult) error {
	if len(result.Items) != len(request.Items) {
		return errors.New("alarmd execution: invalid no-data load result cardinality")
	}
	wanted := make(map[PlanNoDataIdentity]struct{}, len(request.Items))
	for _, item := range request.Items {
		if _, duplicate := wanted[item.Identity]; duplicate {
			return errors.New("alarmd execution: duplicate no-data load request identity")
		}
		wanted[item.Identity] = struct{}{}
	}
	seen := make(map[PlanNoDataIdentity]struct{}, len(result.Items))
	for _, item := range result.Items {
		if _, ok := wanted[item.Identity]; !ok {
			return errors.New("alarmd execution: no-data load returned an unknown identity")
		}
		if _, duplicate := seen[item.Identity]; duplicate {
			return errors.New("alarmd execution: no-data load returned a duplicate identity")
		}
		seen[item.Identity] = struct{}{}
		if err := validateNoDataSnapshot(item); err != nil {
			return err
		}
	}
	return nil
}

func validateNoDataSnapshot(item NoDataMemorySnapshot) error {
	reasonIsNone := item.ReasonCode == "" || item.ReasonCode == observability.ReasonNone
	switch item.Status {
	case NoDataMemoryMissing:
		if noDataSnapshotHasPayload(item) || item.SchemaVersion != 0 || !reasonIsNone {
			return errors.New("alarmd execution: missing no-data memory carries a persisted record")
		}
	case NoDataMemoryFound:
		if item.MarkerRevision == 0 || item.PersistedMutationDigest == "" || !reasonIsNone {
			return errors.New("alarmd execution: found no-data memory requires revision and digest and reason none")
		}
		if item.SchemaVersion == 0 || !NoDataMemoryReadable(item.SchemaVersion) {
			return fmt.Errorf("alarmd execution: found no-data memory has schema %d this build cannot read; "+
				"it is UNREADABLE, not FOUND", item.SchemaVersion)
		}
		if err := item.PersistedApplyVersion.Validate(); err != nil {
			return fmt.Errorf("alarmd execution: invalid no-data apply version: %w", err)
		}
		if item.LastScheduleRevision == "" {
			return errors.New("alarmd execution: found no-data memory requires its last plan schedule revision")
		}
		if item.RosterVersion == "" {
			return errors.New("alarmd execution: found no-data memory requires the roster version it was decided against")
		}
		if item.PresentAsOf < 0 {
			return errors.New("alarmd execution: found no-data memory has a negative present-as-of")
		}
		if item.Representation == NoDataRepresentationNone || item.Representation == "" {
			// A record was read, so something read it. Leaving this unset would
			// make a Plan on the old representation indistinguishable from one
			// with no memory at all, and the count of the first is what says
			// whether the rollout has finished.
			return errors.New("alarmd execution: found no-data memory does not say which record it came from")
		}
		if err := validateNoDataGroups(item.Groups); err != nil {
			return err
		}
		if item.SchemaVersion != NoDataMemorySchemaV2 {
			return nil
		}
		// A v2 record stores a group with no absence as present, and present
		// means exactly the round the header names. A decoded group that claims
		// to have been seen later than the Plan last had data did not come out
		// of that encoding, so the record was decoded wrong or written by
		// something that does not hold the rule.
		for _, group := range item.Groups {
			if group.LastSeen > item.PresentAsOf {
				return fmt.Errorf(
					"alarmd execution: no-data group %q was last seen after the Plan last had data", group.GroupKey)
			}
		}
		return nil
	case NoDataMemoryUnreadable:
		// The schema is the one fact kept, because it is what names the build
		// that wrote the record. Everything else is refused: a payload in a
		// shape this build has no definition for is not evidence, it is a guess
		// with the fields it did not recognise already dropped.
		if noDataSnapshotHasPayload(item) {
			return errors.New("alarmd execution: unreadable no-data memory carries a payload this build cannot read")
		}
		// Schema zero is the second way a record is unreadable, and the two are
		// deliberately one status. A record whose header is gone does not say
		// what shape it is in, so this build cannot read it for the same reason
		// it cannot read a newer one, and the right thing to do with it is the
		// same: leave it exactly as it is and pause this Plan's no-data
		// detection. Reading it anyway would take each group's last-seen time
		// from a round nobody stated.
		if item.SchemaVersion != 0 && NoDataMemoryReadable(item.SchemaVersion) {
			return fmt.Errorf("alarmd execution: no-data memory schema %d is readable by this build and must not be "+
				"reported unreadable", item.SchemaVersion)
		}
		if err := requireReasonClass(item.ReasonCode, contract.ReasonClassDeterministic); err != nil {
			return err
		}
	case NoDataMemoryUnavailable:
		if noDataSnapshotHasPayload(item) {
			return errors.New("alarmd execution: unavailable no-data memory carries trusted payload")
		}
		if err := requireReasonClass(item.ReasonCode, contract.ReasonClassRetryable); err != nil {
			return err
		}
	case NoDataMemoryTerminal:
		if noDataSnapshotHasPayload(item) {
			return errors.New("alarmd execution: terminal no-data memory carries trusted payload")
		}
		if err := requireReasonClass(item.ReasonCode, contract.ReasonClassDeterministic); err != nil {
			return err
		}
	default:
		return errors.New("alarmd execution: unknown no-data load status")
	}
	return nil
}

func validateNoDataGroups(groups []NoDataGroupMemory) error {
	seen := make(map[string]struct{}, len(groups))
	previous := ""
	for index, group := range groups {
		if group.GroupKey == "" {
			return errors.New("alarmd execution: stored no-data group requires a key")
		}
		if _, duplicate := seen[group.GroupKey]; duplicate {
			return errors.New("alarmd execution: stored no-data memory contains a duplicate group")
		}
		seen[group.GroupKey] = struct{}{}
		if index > 0 && group.GroupKey < previous {
			return errors.New("alarmd execution: stored no-data memory is not in canonical group order")
		}
		previous = group.GroupKey
		if group.LastSeen < 0 || group.FirstAbsent < 0 {
			return errors.New("alarmd execution: stored no-data timestamps must not be negative")
		}
		if group.LastSeen == 0 && group.FirstAbsent == 0 {
			return errors.New("alarmd execution: stored no-data group remembers nothing")
		}
	}
	return nil
}
