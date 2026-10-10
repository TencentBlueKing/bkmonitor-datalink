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

// BuildPlanGapMutation canonicalizes a bounded Plan gap update and owns its
// digest. The digest closes the payload used by GapStore idempotency.
func BuildPlanGapMutation(mutation PlanGapMutation) (PlanGapMutation, error) {
	if mutation.MutationDigest != "" {
		return PlanGapMutation{}, errors.New("alarmd execution: Plan gap mutation builder owns the digest")
	}
	mutation = normalizePlanGapMutation(mutation)
	digest, err := derivePlanGapMutationDigest(mutation)
	if err != nil {
		return PlanGapMutation{}, err
	}
	mutation.MutationDigest = digest
	return mutation, nil
}

func (mutation PlanGapMutation) ValidateDigest() error {
	if mutation.MutationDigest == "" || !planGapMutationIsCanonical(mutation) {
		return errors.New("alarmd execution: Plan gap mutation is not canonical")
	}
	expected, err := derivePlanGapMutationDigest(mutation)
	if err != nil {
		return err
	}
	if mutation.MutationDigest != expected {
		return errors.New("alarmd execution: Plan gap mutation digest does not match its payload")
	}
	return nil
}

func derivePlanGapMutationDigest(mutation PlanGapMutation) (MutationDigest, error) {
	if mutation.Identity.Plan.Validate() != nil || mutation.Identity.StateGeneration == "" ||
		mutation.ScheduleRevision == "" || len(mutation.Scopes) == 0 {
		return "", errors.New("alarmd execution: incomplete Plan gap mutation")
	}
	if err := mutation.ApplyVersion.Validate(); err != nil {
		return "", err
	}
	seen := make(map[GapScope]struct{}, len(mutation.Scopes))
	for _, scope := range mutation.Scopes {
		if err := validateOptionalLevel(scope.Scope.LevelID, scope.Scope.HasLevel); err != nil {
			return "", err
		}
		if _, duplicate := seen[scope.Scope]; duplicate {
			return "", errors.New("alarmd execution: duplicate Plan gap mutation scope")
		}
		seen[scope.Scope] = struct{}{}
		switch scope.Kind {
		case GapOpen, GapStrengthen, GapWarmup:
			if scope.ReasonCode == "" || scope.RequiredFullSlots == 0 {
				return "", errors.New("alarmd execution: non-clear Plan gap mutation requires a reason and positive warmup target")
			}
		case GapClear:
			if scope.ReasonCode != "" || scope.RequiredFullSlots != 0 {
				return "", errors.New("alarmd execution: clear Plan gap mutation must not carry reason or warmup target")
			}
		default:
			return "", errors.New("alarmd execution: invalid Plan gap mutation kind")
		}
	}
	type digestScope struct {
		Scope             GapScope   `json:"scope"`
		Effect            string     `json:"effect"`
		ReasonCode        ReasonCode `json:"reason_code,omitempty"`
		RequiredFullSlots uint32     `json:"required_full_slots,omitempty"`
	}
	digestScopes := make([]digestScope, len(mutation.Scopes))
	for index, scope := range mutation.Scopes {
		effect := string(scope.Kind)
		if scope.Kind == GapOpen || scope.Kind == GapStrengthen {
			effect = "ENSURE_GAPPED"
		}
		digestScopes[index] = digestScope{
			Scope: scope.Scope, Effect: effect, ReasonCode: scope.ReasonCode,
			RequiredFullSlots: scope.RequiredFullSlots,
		}
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-plan-gap-mutation-v1", struct {
		Identity         PlanGapIdentity      `json:"identity"`
		ApplyVersion     ApplyVersion         `json:"apply_version"`
		ScheduleRevision PlanScheduleRevision `json:"schedule_revision"`
		Scopes           []digestScope        `json:"scopes"`
	}{mutation.Identity, mutation.ApplyVersion, mutation.ScheduleRevision, digestScopes})
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive Plan gap mutation digest: %w", err)
	}
	return MutationDigest(digest), nil
}

func normalizePlanGapMutation(mutation PlanGapMutation) PlanGapMutation {
	mutation.Scopes = append([]GapScopeMutation(nil), mutation.Scopes...)
	sort.Slice(mutation.Scopes, func(left, right int) bool {
		if mutation.Scopes[left].Scope.HasLevel != mutation.Scopes[right].Scope.HasLevel {
			return !mutation.Scopes[left].Scope.HasLevel
		}
		return mutation.Scopes[left].Scope.LevelID < mutation.Scopes[right].Scope.LevelID
	})
	return mutation
}

func planGapMutationIsCanonical(mutation PlanGapMutation) bool {
	normalized := normalizePlanGapMutation(mutation)
	if len(normalized.Scopes) != len(mutation.Scopes) {
		return false
	}
	for index := range mutation.Scopes {
		if mutation.Scopes[index] != normalized.Scopes[index] {
			return false
		}
	}
	return true
}

// ExtendSameSlotGap adds protection without replaying an already recorded Slot.
// Existing observations and reasons belong to the first committed attempt.
// In particular this operation never warms, clears, or resets those facts.
// Callers validate the inputs and fence identity, ApplyVersion and schedule;
// persistence must still compare the marker revision and the stored value.
func ExtendSameSlotGap(previous []GapScopeState, mutations []GapScopeMutation) ([]GapScopeState, bool) {
	if len(previous) == 0 || len(mutations) == 0 {
		return nil, false
	}
	next := append([]GapScopeState(nil), previous...)
	changed := false
	for _, mutation := range mutations {
		if mutation.Kind != GapOpen && mutation.Kind != GapStrengthen {
			return nil, false
		}
		index := -1
		for i := range next {
			if next[i].Scope == mutation.Scope {
				index = i
				break
			}
		}
		if index < 0 {
			next = append(next, GapScopeState{Scope: mutation.Scope, Status: GapStatusGapped, ReasonCode: mutation.ReasonCode, RequiredFullSlots: mutation.RequiredFullSlots})
			changed = true
			continue
		}
		current := &next[index]
		if mutation.RequiredFullSlots < current.RequiredFullSlots || mutation.ReasonCode != current.ReasonCode {
			return nil, false
		}
		if current.Status != GapStatusGapped || mutation.RequiredFullSlots > current.RequiredFullSlots {
			changed = true
		}
		// A same-Slot retry preserves the first commit's observations, so
		// GAPPED may retain a positive count. It does not count a new Slot;
		// a new-Slot gap is applied separately and resets that count.
		current.Status = GapStatusGapped
		current.RequiredFullSlots = mutation.RequiredFullSlots
	}
	if !changed {
		return nil, false
	}
	sort.Slice(next, func(i, j int) bool {
		if next[i].Scope.HasLevel != next[j].Scope.HasLevel {
			return !next[i].Scope.HasLevel
		}
		return next[i].Scope.LevelID < next[j].Scope.LevelID
	})
	return next, true
}
