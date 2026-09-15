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
