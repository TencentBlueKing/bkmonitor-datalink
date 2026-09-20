// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"errors"
	"fmt"
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
	// NoDataMemorySchemaV1 stores per-group timestamps and nothing else.
	NoDataMemorySchemaV1 NoDataMemorySchema = 1
	// MaxSupportedNoDataMemorySchema is the newest record shape this build can
	// read. A record above it was written by a newer build than this one, which
	// happens during a rollback, and the rule for it is in NoDataMemoryReadable.
	MaxSupportedNoDataMemorySchema = NoDataMemorySchemaV1
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

// BuildPlanNoDataMutation canonicalizes a whole no-data memory replacement and
// owns its digest. The digest closes the payload used for store idempotency.
//
// The mutation is the memory, not a change to it: applying it replaces every
// group the Plan remembers. A Slot that judged absence knows the complete set -
// the roster it expected plus whatever unexpected groups reported - so sending
// the set is both smaller to reason about and impossible to apply by halves.
//
// An empty group list is a legitimate mutation and means the Plan now remembers
// nothing, which is what a Slot produces when the last group it knew about left
// the business. It is not the same as sending no mutation at all: no mutation
// means the memory is not to be touched this round, which is what a Slot that
// did not see its whole period must send.
func BuildPlanNoDataMutation(mutation PlanNoDataMutation) (PlanNoDataMutation, error) {
	if mutation.MutationDigest != "" {
		return PlanNoDataMutation{}, errors.New("alarmd execution: Plan no-data mutation builder owns the digest")
	}
	mutation = normalizePlanNoDataMutation(mutation)
	digest, err := derivePlanNoDataMutationDigest(mutation)
	if err != nil {
		return PlanNoDataMutation{}, err
	}
	mutation.MutationDigest = digest
	return mutation, nil
}

func (mutation PlanNoDataMutation) ValidateDigest() error {
	if mutation.MutationDigest == "" || !planNoDataMutationIsCanonical(mutation) {
		return errors.New("alarmd execution: Plan no-data mutation is not canonical")
	}
	expected, err := derivePlanNoDataMutationDigest(mutation)
	if err != nil {
		return err
	}
	if mutation.MutationDigest != expected {
		return errors.New("alarmd execution: Plan no-data mutation digest does not match its payload")
	}
	return nil
}

func derivePlanNoDataMutationDigest(mutation PlanNoDataMutation) (MutationDigest, error) {
	if mutation.Identity.Plan.Validate() != nil || mutation.Identity.StateGeneration == "" ||
		mutation.ScheduleRevision == "" {
		return "", errors.New("alarmd execution: incomplete Plan no-data mutation")
	}
	if mutation.SchemaVersion == 0 || !NoDataMemoryReadable(mutation.SchemaVersion) {
		return "", fmt.Errorf("alarmd execution: Plan no-data mutation schema %d is not writable by this build",
			mutation.SchemaVersion)
	}
	if mutation.RosterVersion == "" {
		return "", errors.New("alarmd execution: Plan no-data mutation requires the roster version it was decided against")
	}
	if err := mutation.ApplyVersion.Validate(); err != nil {
		return "", err
	}
	seen := make(map[string]struct{}, len(mutation.Groups))
	for _, group := range mutation.Groups {
		if group.GroupKey == "" {
			return "", errors.New("alarmd execution: Plan no-data mutation group requires a key")
		}
		if _, duplicate := seen[group.GroupKey]; duplicate {
			return "", errors.New("alarmd execution: duplicate Plan no-data mutation group")
		}
		seen[group.GroupKey] = struct{}{}
		if group.LastSeen < 0 || group.FirstAbsent < 0 {
			return "", errors.New("alarmd execution: Plan no-data mutation timestamps must not be negative")
		}
		if group.LastSeen == 0 && group.FirstAbsent == 0 {
			return "", errors.New("alarmd execution: Plan no-data mutation group remembers nothing and must be omitted")
		}
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-plan-no-data-mutation-v1", struct {
		Identity         PlanNoDataIdentity   `json:"identity"`
		SchemaVersion    NoDataMemorySchema   `json:"schema_version"`
		ApplyVersion     ApplyVersion         `json:"apply_version"`
		ScheduleRevision PlanScheduleRevision `json:"schedule_revision"`
		RosterVersion    string               `json:"roster_version"`
		Groups           []NoDataGroupMemory  `json:"groups"`
	}{
		mutation.Identity, mutation.SchemaVersion, mutation.ApplyVersion,
		mutation.ScheduleRevision, mutation.RosterVersion, mutation.Groups,
	})
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive Plan no-data mutation digest: %w", err)
	}
	return MutationDigest(digest), nil
}

func normalizePlanNoDataMutation(mutation PlanNoDataMutation) PlanNoDataMutation {
	mutation.Groups = append([]NoDataGroupMemory(nil), mutation.Groups...)
	sort.Slice(mutation.Groups, func(left, right int) bool {
		return mutation.Groups[left].GroupKey < mutation.Groups[right].GroupKey
	})
	return mutation
}

func planNoDataMutationIsCanonical(mutation PlanNoDataMutation) bool {
	normalized := normalizePlanNoDataMutation(mutation)
	if len(normalized.Groups) != len(mutation.Groups) {
		return false
	}
	for index := range mutation.Groups {
		if normalized.Groups[index] != mutation.Groups[index] {
			return false
		}
	}
	return true
}
