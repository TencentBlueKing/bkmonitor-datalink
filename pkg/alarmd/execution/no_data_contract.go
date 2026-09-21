// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// NoDataMemorySchema versions the stored no-data memory record.
//
// It is here from the first version rather than added when a second one is
// needed, because the version has to be readable from a record written before
// anyone knew a second version would exist. A record with no version field is
// not a version-1 record; it is a record whose shape has to be guessed.
type NoDataMemorySchema uint32

const (
	// NoDataMemorySchemaV1 stores per-group timestamps and nothing else, all of
	// them in one encoded value. This build still reads it and never writes it.
	NoDataMemorySchemaV1 NoDataMemorySchema = 1
	// NoDataMemorySchemaV2 stores one field per group beside a header field,
	// and is written as a delta.
	//
	// The change is not an optimisation. A whole memory in one value has a
	// largest size it may reach, and the set of groups a Plan remembers only
	// grows, so every Plan with enough groups eventually reaches it and stops
	// remembering anything - at the moment absence is being detected, which is
	// when each group's record grows by its first-absent time. One field per
	// group is the shape the platform's own Python cache has always had, and it
	// has no such ceiling.
	NoDataMemorySchemaV2 NoDataMemorySchema = 2
	// MaxSupportedNoDataMemorySchema is the newest record shape this build can
	// read. A record above it was written by a newer build than this one, which
	// happens during a rollback, and the rule for it is in NoDataMemoryReadable.
	MaxSupportedNoDataMemorySchema = NoDataMemorySchemaV2
	// WrittenNoDataMemorySchema is the one shape this build writes. Reading two
	// and writing one is the whole coexistence rule: a Plan whose record is
	// still v1 is read from it and written to v2, and no build ever writes both
	// so no reader has to merge them.
	WrittenNoDataMemorySchema = NoDataMemorySchemaV2
)

// NoDataMemoryReadable says whether this build may load and replace a stored
// no-data memory record of the given schema.
//
// An unreadable record is left exactly as it is: not loaded, not applied to,
// not cleared. That is the whole rollout rule in one sentence, and the reason
// it works is that the record holds timestamps rather than a count of rounds.
// A build that skips a Plan for a hundred rounds and then reads the record
// again derives the same absence duration a build that ran every one of those
// rounds would derive, because the duration comes from when the group was last
// seen and when it was first called absent - not from how many times anyone
// looked. Clearing the record instead would restart the clock and hide an
// outage that had been running the whole time; writing to it in a shape the
// writer does not understand would corrupt it for the build that does.
//
// Schema zero is not a record at all - it is the absence of one, which is the
// normal state before a Plan's first no-data round - so it reads as readable
// and loads as empty memory.
//
// One consequence, stated here so it is not mistaken for a defect later. When
// the pause ends and the record is readable again, a group that was absent
// throughout reports a duration that includes the pause, because the duration
// is measured from FirstAbsent and that is when the absence began. It is
// correct: the data really was missing for that whole span, and nobody was
// watching is not the same as nothing was wrong. Counting rounds instead would
// report the shorter number - the rounds that happened to run - which is the
// under-report this design exists to avoid.
func NoDataMemoryReadable(schema NoDataMemorySchema) bool {
	return schema <= MaxSupportedNoDataMemorySchema
}

// PlanNoDataMemoryUpdate is one round's whole statement about a Plan's no-data
// memory: the memory it now holds, and the record it read to get there.
//
// The delta and the digest are both derived from this, by the builder, and that
// is the point of passing it rather than passing them. They have to agree - the
// digest names the memory the delta results in - and nothing in the mutation
// can check that they do, because the mutation deliberately does not carry the
// memory. One input with two derived products cannot disagree; two inputs the
// caller fills in separately eventually do, and the disagreement is a memory
// that reads as already applied while holding something else.
type PlanNoDataMemoryUpdate struct {
	Identity PlanNoDataIdentity
	// DerivedFrom is which record Loaded and ExpectedMarkerRevision came out
	// of. See PlanNoDataMutation.DerivedFrom: it decides both whether the
	// expected revision means anything to the record being written and whether
	// the statement is a delta or the whole memory.
	DerivedFrom            NoDataRepresentation
	ExpectedMarkerRevision uint64
	// LoadedApplyVersion is the apply version of the record this round read,
	// zero when it read none. See PlanNoDataMutation.LoadedApplyVersion.
	LoadedApplyVersion ApplyVersion
	ApplyVersion       ApplyVersion
	ScheduleRevision   PlanScheduleRevision
	RosterVersion      string
	// PresentAsOf is the round this Plan last had data in: this round's time
	// when anything reported, and otherwise LoadedPresentAsOf carried forward.
	PresentAsOf int64
	// Memory is the whole memory after this round, in any order.
	Memory []NoDataGroupMemory
	// Loaded and LoadedPresentAsOf are the record this round read. Both are
	// needed: the delta is what changed in the stored values, and a group's
	// stored value depends on the record's present-as-of as well as on its own
	// timestamps, so a group can keep both its timestamps and still need
	// writing because the round it is being compressed against moved.
	Loaded            []NoDataGroupMemory
	LoadedPresentAsOf int64
}

// BuildPlanNoDataMutation derives one round's delta and the identity of the
// memory it results in.
//
// An empty delta is a legitimate mutation only in the sense that the caller
// should not send one: a round that changes nothing has nothing to write, and
// the caller checks that before building. It is not rejected here, because
// "the memory is already what this round would write" is a decision about
// whether to spend a mutation, not a property of the statement.
func BuildPlanNoDataMutation(update PlanNoDataMemoryUpdate) (PlanNoDataMutation, error) {
	memory, err := canonicalNoDataMemory(update.Memory)
	if err != nil {
		return PlanNoDataMutation{}, err
	}
	loaded, err := canonicalNoDataMemory(update.Loaded)
	if err != nil {
		return PlanNoDataMutation{}, fmt.Errorf("alarmd execution: loaded no-data memory is not storable: %w", err)
	}
	if update.PresentAsOf < 0 || update.LoadedPresentAsOf < 0 {
		return PlanNoDataMutation{}, errors.New("alarmd execution: no-data present-as-of must not be negative")
	}
	if update.PresentAsOf < update.LoadedPresentAsOf {
		// The Plan cannot have last had data earlier than the record already
		// says it did. A round that saw nothing carries the stored value
		// forward; one that moved it backwards would make every group stored
		// without an absence claim a last-seen time before the one it had.
		return PlanNoDataMutation{}, fmt.Errorf(
			"alarmd execution: no-data present-as-of %d is older than the record's %d",
			update.PresentAsOf, update.LoadedPresentAsOf)
	}
	for _, group := range memory {
		if group.LastSeen > update.PresentAsOf {
			// A group seen after the Plan last had data is not a memory anyone
			// can store: the round it names is the last-seen time of every
			// group stored without an absence, so a group ahead of it would
			// have to be written the long way to be written at all, and the
			// two would then disagree about when this Plan last reported.
			return PlanNoDataMutation{}, fmt.Errorf(
				"alarmd execution: no-data group %q was last seen at %d, after the Plan last had data at %d",
				group.GroupKey, group.LastSeen, update.PresentAsOf)
		}
	}
	if len(memory) > math.MaxUint32 {
		return PlanNoDataMutation{}, errors.New("alarmd execution: no-data memory holds more groups than can be counted")
	}
	mutation := PlanNoDataMutation{
		Identity:               update.Identity,
		SchemaVersion:          WrittenNoDataMemorySchema,
		DerivedFrom:            update.DerivedFrom,
		LoadedApplyVersion:     update.LoadedApplyVersion,
		ExpectedMarkerRevision: update.ExpectedMarkerRevision,
		ApplyVersion:           update.ApplyVersion,
		ScheduleRevision:       update.ScheduleRevision,
		RosterVersion:          update.RosterVersion,
		PresentAsOf:            update.PresentAsOf,
		GroupCount:             uint32(len(memory)),
	}
	if mutation.ReplacesWholeRecord() {
		// Nothing to take a difference against. What was loaded describes
		// another record -- the whole-memory one, or no record at all -- so the
		// statement carries every group and the store replaces what it holds.
		// Taking the difference anyway is the shape of the defect this guards:
		// the groups that did not change since the blob would be left out, and
		// the per-group record would be written holding only the ones that did.
		loaded = nil
	}
	mutation.Set, mutation.Del = noDataMemoryDelta(memory, update.PresentAsOf, loaded, update.LoadedPresentAsOf)
	memoryDigest, err := derivePlanNoDataMemoryDigest(mutation, memory)
	if err != nil {
		return PlanNoDataMutation{}, err
	}
	mutation.MemoryDigest = memoryDigest
	statement, err := derivePlanNoDataStatementDigest(mutation)
	if err != nil {
		return PlanNoDataMutation{}, err
	}
	mutation.MutationDigest = statement
	return mutation, nil
}

// ValidateDigest checks the whole payload the store was handed.
//
// The memory itself is not here and cannot be recomputed - that is the point of
// sending a delta - but it does not have to be: the statement digest covers the
// memory digest, so a payload whose memory digest was changed, or whose delta
// was, fails this. What the store cannot check is whether the memory digest is
// the digest of the memory the writer actually held; that is the writer's to
// get right, and the builder is the only thing that produces one.
func (mutation PlanNoDataMutation) ValidateDigest() error {
	if mutation.MutationDigest == "" || mutation.MemoryDigest == "" {
		return errors.New("alarmd execution: Plan no-data mutation is not canonical")
	}
	if err := mutation.validateStatement(); err != nil {
		return err
	}
	expected, err := derivePlanNoDataStatementDigest(mutation)
	if err != nil {
		return err
	}
	if mutation.MutationDigest != expected {
		return errors.New("alarmd execution: Plan no-data mutation digest does not match its payload")
	}
	return nil
}

// derivePlanNoDataStatementDigest is the identity of one write: everything on
// the wire, including the digest of the memory it claims to produce.
func derivePlanNoDataStatementDigest(mutation PlanNoDataMutation) (MutationDigest, error) {
	if err := mutation.validateStatement(); err != nil {
		return "", err
	}
	if mutation.MemoryDigest == "" {
		return "", errors.New("alarmd execution: Plan no-data mutation states no resulting memory")
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-plan-no-data-statement-v2", struct {
		Identity               PlanNoDataIdentity   `json:"identity"`
		SchemaVersion          NoDataMemorySchema   `json:"schema_version"`
		DerivedFrom            NoDataRepresentation `json:"derived_from"`
		ExpectedMarkerRevision uint64               `json:"expected_marker_revision"`
		LoadedApplyVersion     ApplyVersion         `json:"loaded_apply_version"`
		ApplyVersion           ApplyVersion         `json:"apply_version"`
		ScheduleRevision       PlanScheduleRevision `json:"schedule_revision"`
		RosterVersion          string               `json:"roster_version"`
		PresentAsOf            int64                `json:"present_as_of"`
		MemoryDigest           MutationDigest       `json:"memory_digest"`
		GroupCount             uint32               `json:"group_count"`
		Set                    []NoDataGroupDelta   `json:"set"`
		Del                    []string             `json:"del"`
	}{
		mutation.Identity, mutation.SchemaVersion, mutation.DerivedFrom, mutation.ExpectedMarkerRevision,
		mutation.LoadedApplyVersion, mutation.ApplyVersion,
		mutation.ScheduleRevision, mutation.RosterVersion, mutation.PresentAsOf, mutation.MemoryDigest,
		mutation.GroupCount, mutation.Set, mutation.Del,
	})
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive Plan no-data statement digest: %w", err)
	}
	return MutationDigest(digest), nil
}

func (mutation PlanNoDataMutation) validateStatement() error {
	if mutation.Identity.Plan.Validate() != nil || mutation.Identity.StateGeneration == "" ||
		mutation.ScheduleRevision == "" {
		return errors.New("alarmd execution: incomplete Plan no-data mutation")
	}
	if mutation.SchemaVersion != WrittenNoDataMemorySchema {
		return fmt.Errorf("alarmd execution: Plan no-data mutation schema %d is not writable by this build",
			mutation.SchemaVersion)
	}
	switch mutation.DerivedFrom {
	case NoDataRepresentationNone, NoDataRepresentationWholeMemory, NoDataRepresentationPerGroup:
	default:
		// Not defaulted to the per-group value. That default would make an
		// unset field mean "this is a delta against the stored record", which
		// is the one reading that silently drops groups, and it would make the
		// field's absence indistinguishable from a caller that meant it.
		return fmt.Errorf(
			"alarmd execution: Plan no-data mutation does not say which record it was derived from (%q)",
			mutation.DerivedFrom)
	}
	if mutation.ReplacesWholeRecord() && len(mutation.Del) != 0 {
		return errors.New(
			"alarmd execution: a Plan no-data mutation that replaces the record has nothing to delete from it")
	}
	// The loaded version goes with the record: a statement derived from one
	// names the version it read, and one derived from none names nothing. A
	// zero beside a named record would let the store's "did the record move
	// since the read" comparison pass on a value nobody read.
	if mutation.DerivedFrom == NoDataRepresentationNone {
		if mutation.LoadedApplyVersion != (ApplyVersion{}) {
			return errors.New("alarmd execution: a Plan no-data mutation derived from no record cannot name a loaded version")
		}
		if mutation.ExpectedMarkerRevision != 0 {
			return errors.New("alarmd execution: a Plan no-data mutation derived from no record cannot expect a revision")
		}
	} else {
		if err := mutation.LoadedApplyVersion.Validate(); err != nil {
			return fmt.Errorf("alarmd execution: a Plan no-data mutation derived from a record must name the version it read: %w", err)
		}
		// Every record found carries a revision of one or more; a statement
		// that read one and expects zero is the shape the store would apply as
		// a delta to a record it believes absent -- a per-group record written
		// holding only the groups that changed. Not reachable today, because
		// the decoders refuse a found record at revision zero, and refused
		// here so that it stays unreachable when they change.
		if mutation.ExpectedMarkerRevision == 0 {
			return errors.New("alarmd execution: a Plan no-data mutation derived from a record must expect its revision")
		}
	}
	if mutation.RosterVersion == "" {
		return errors.New("alarmd execution: Plan no-data mutation requires the roster version it was decided against")
	}
	if err := mutation.ApplyVersion.Validate(); err != nil {
		return err
	}
	if mutation.PresentAsOf < 0 {
		return errors.New("alarmd execution: no-data present-as-of must not be negative")
	}
	touched := make(map[string]struct{}, len(mutation.Set)+len(mutation.Del))
	previous := ""
	for index, group := range mutation.Set {
		if group.GroupKey == "" {
			return errors.New("alarmd execution: Plan no-data mutation group requires a key")
		}
		if index > 0 && group.GroupKey <= previous {
			return errors.New("alarmd execution: Plan no-data mutation groups are not in canonical order")
		}
		previous = group.GroupKey
		touched[group.GroupKey] = struct{}{}
		if group.Absent == nil {
			if mutation.PresentAsOf == 0 {
				return errors.New(
					"alarmd execution: a Plan that has never had data cannot store a group as present")
			}
			continue
		}
		if group.Absent.LastSeen < 0 || group.Absent.FirstAbsent < 0 {
			return errors.New("alarmd execution: Plan no-data mutation timestamps must not be negative")
		}
		if group.Absent.LastSeen == 0 && group.Absent.FirstAbsent == 0 {
			return errors.New("alarmd execution: Plan no-data mutation group remembers nothing and must be omitted")
		}
		if group.Absent.FirstAbsent == 0 && group.Absent.LastSeen == mutation.PresentAsOf {
			// Both encodings would read back the same, and two ways to say one
			// thing is two digests for one memory.
			return errors.New("alarmd execution: a group last seen in the present round must be stored as present")
		}

	}
	previous = ""
	for index, key := range mutation.Del {
		if key == "" {
			return errors.New("alarmd execution: Plan no-data mutation delete requires a key")
		}
		if index > 0 && key <= previous {
			return errors.New("alarmd execution: Plan no-data mutation deletes are not in canonical order")
		}
		previous = key
		if _, both := touched[key]; both {
			return fmt.Errorf("alarmd execution: Plan no-data mutation both writes and deletes group %q", key)
		}
	}
	if int(mutation.GroupCount) < len(mutation.Set) {
		return errors.New("alarmd execution: Plan no-data memory holds fewer groups than the mutation writes")
	}
	return nil
}

// noDataMemoryDelta is what changed in the stored values, not in the
// timestamps.
//
// The difference decides the whole representation. A group present in both
// rounds keeps the same stored value while its last-seen time advances with the
// Plan's, and writing it would be writing the same byte back once per group per
// round - which is the cost this change exists to remove. A group whose stored
// value changes is written whether or not its timestamps did.
func noDataMemoryDelta(
	memory []NoDataGroupMemory, presentAsOf int64, loaded []NoDataGroupMemory, loadedPresentAsOf int64,
) ([]NoDataGroupDelta, []string) {
	previous := make(map[string]NoDataGroupDelta, len(loaded))
	for _, group := range loaded {
		previous[group.GroupKey] = noDataGroupValue(group, loadedPresentAsOf)
	}
	var set []NoDataGroupDelta
	kept := make(map[string]struct{}, len(memory))
	for _, group := range memory {
		kept[group.GroupKey] = struct{}{}
		value := noDataGroupValue(group, presentAsOf)
		if before, stored := previous[group.GroupKey]; stored && sameNoDataGroupValue(before, value) {
			continue
		}
		set = append(set, value)
	}
	var deleted []string
	for _, group := range loaded {
		if _, still := kept[group.GroupKey]; !still {
			deleted = append(deleted, group.GroupKey)
		}
	}
	return set, deleted
}

// noDataGroupValue is how one group is stored against a given present-as-of.
//
// Present is the compressed form and it is only reachable for a group whose
// last-seen time is exactly the round being compressed against. Everything else
// - an absence, and equally a group the roster stopped expecting while it was
// present and which therefore kept an older last-seen - is written in full.
func noDataGroupValue(group NoDataGroupMemory, presentAsOf int64) NoDataGroupDelta {
	if group.FirstAbsent == 0 && presentAsOf > 0 && group.LastSeen == presentAsOf {
		return NoDataGroupDelta{GroupKey: group.GroupKey}
	}
	return NoDataGroupDelta{
		GroupKey: group.GroupKey,
		Absent:   &NoDataGroupAbsence{LastSeen: group.LastSeen, FirstAbsent: group.FirstAbsent},
	}
}

func sameNoDataGroupValue(left, right NoDataGroupDelta) bool {
	if left.GroupKey != right.GroupKey {
		return false
	}
	if left.Absent == nil || right.Absent == nil {
		return left.Absent == nil && right.Absent == nil
	}
	return *left.Absent == *right.Absent
}

// canonicalNoDataMemory is a whole memory in the one order a digest may be
// taken over, with every group checked for storability.
func canonicalNoDataMemory(groups []NoDataGroupMemory) ([]NoDataGroupMemory, error) {
	canonical := append([]NoDataGroupMemory(nil), groups...)
	sort.Slice(canonical, func(left, right int) bool {
		return canonical[left].GroupKey < canonical[right].GroupKey
	})
	previous := ""
	for index, group := range canonical {
		if group.GroupKey == "" {
			return nil, errors.New("alarmd execution: Plan no-data mutation group requires a key")
		}
		if index > 0 && group.GroupKey == previous {
			return nil, errors.New("alarmd execution: duplicate Plan no-data mutation group")
		}
		previous = group.GroupKey
		if group.LastSeen < 0 || group.FirstAbsent < 0 {
			return nil, errors.New("alarmd execution: Plan no-data mutation timestamps must not be negative")
		}
		if group.LastSeen == 0 && group.FirstAbsent == 0 {
			return nil, errors.New("alarmd execution: Plan no-data mutation group remembers nothing and must be omitted")
		}
	}
	return canonical, nil
}

// derivePlanNoDataMemoryDigest is the identity of the memory a mutation results
// in.
//
// It covers the memory and the facts that make it that memory, and not the
// apply version: the store's idempotency check is the pair (apply version,
// digest), and a digest that already held the version could never answer the
// other question the pair is there for - whether two records with different
// versions hold the same memory. It does not cover the delta either, so two
// rounds reaching one memory from different records agree, which is what keeps
// a retry from reading as a conflict.
func derivePlanNoDataMemoryDigest(
	mutation PlanNoDataMutation, memory []NoDataGroupMemory,
) (MutationDigest, error) {
	if err := mutation.validateStatement(); err != nil {
		return "", err
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-plan-no-data-memory-v2", struct {
		Identity         PlanNoDataIdentity   `json:"identity"`
		SchemaVersion    NoDataMemorySchema   `json:"schema_version"`
		ScheduleRevision PlanScheduleRevision `json:"schedule_revision"`
		RosterVersion    string               `json:"roster_version"`
		PresentAsOf      int64                `json:"present_as_of"`
		Groups           []NoDataGroupMemory  `json:"groups"`
	}{
		mutation.Identity, mutation.SchemaVersion, mutation.ScheduleRevision,
		mutation.RosterVersion, mutation.PresentAsOf, memory,
	})
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive Plan no-data memory digest: %w", err)
	}
	return MutationDigest(digest), nil
}
