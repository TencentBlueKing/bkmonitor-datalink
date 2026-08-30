// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"errors"
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type IdentityError struct{ Err error }

func (err *IdentityError) Error() string {
	return fmt.Sprintf("state: deterministic-invalid identity: %v", err.Err)
}
func (err *IdentityError) Unwrap() error { return err.Err }

func identityError(message string) error { return &IdentityError{Err: errors.New(message)} }

func RuntimeStateKeyV2(prefix string, identity execution.StateKeyIdentity) (string, error) {
	if err := validatePlanIdentity(prefix, identity.Plan, identity.StateGeneration); err != nil {
		return "", err
	}
	if identity.SeriesIdentityDigest == "" {
		return "", identityError("series identity digest is required")
	}
	series, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-series-v2", identity.SeriesIdentityDigest)
	if err != nil {
		return "", fmt.Errorf("state: derive series digest: %w", err)
	}
	return executionKey(prefix, "runtime", identity.Plan, identity.StateGeneration, series), nil
}

func PlanGapKeyV2(prefix string, identity execution.PlanGapIdentity) (string, error) {
	if err := validatePlanIdentity(prefix, identity.Plan, identity.StateGeneration); err != nil {
		return "", err
	}
	return executionKey(prefix, "gap", identity.Plan, identity.StateGeneration, ""), nil
}

func validatePlanIdentity(prefix string, plan execution.PlanIdentity, generation execution.StateGeneration) error {
	if strings.TrimSpace(prefix) == "" || len(prefix) > 64 || strings.ContainsAny(prefix, "{} \t\r\n") {
		return identityError("valid key prefix is required")
	}
	if err := plan.Validate(); err != nil {
		return &IdentityError{Err: err}
	}
	if !isCanonicalSignedInt64(plan.BusinessID) {
		return identityError("business identity must use canonical signed int64 form")
	}
	if !isCanonicalUint64(plan.StrategyID) {
		return identityError("strategy identity must use canonical unsigned uint64 form")
	}
	if generation == "" {
		return identityError("state generation is required")
	}
	return nil
}

func executionKey(prefix, kind string, plan execution.PlanIdentity, generation execution.StateGeneration, suffix string) string {
	tenant, _ := contract.DeriveCanonicalDigestV2("alarmd-runtime-tenant-v2", plan.TenantID)
	generationDigest, _ := contract.DeriveCanonicalDigestV2("alarmd-state-generation-v2", generation)
	parts := []string{prefix, kind, "v2", tenant[:32], plan.BusinessID, plan.StrategyID, generationDigest[:32]}
	if suffix != "" {
		parts = append(parts, suffix[:32])
	}
	return strings.Join(parts, ":")
}
