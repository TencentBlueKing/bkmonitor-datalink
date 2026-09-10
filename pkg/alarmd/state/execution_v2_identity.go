// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"errors"
	"fmt"
	"strings"
	"sync"

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

// A Runtime State key is derived once per series on every Slot, but two of the
// three digests inside it are process-wide near-constants: one per tenant and
// one per compiled state generation. Remembering those leaves only the series
// digest, which is genuinely per key, to be derived.
var (
	tenantKeySegments     = newKeySegmentMemo("alarmd-runtime-tenant-v2")
	generationKeySegments = newKeySegmentMemo("alarmd-state-generation-v2")
)

// keySegmentMemo remembers the digest of a low cardinality identity string. It
// clears rather than evicts: the populations it holds are bounded by
// configuration, so reaching the bound means that assumption no longer holds
// and starting over is better than growing without limit. An underivable
// digest is not remembered and is returned as it was, so the caller slices it
// and fails exactly where it did before.
type keySegmentMemo struct {
	domain  string
	mu      sync.RWMutex
	digests map[string]string
}

const keySegmentMemoEntries = 4096

func newKeySegmentMemo(domain string) *keySegmentMemo {
	return &keySegmentMemo{domain: domain, digests: make(map[string]string)}
}

func (memo *keySegmentMemo) digest(value string) string {
	memo.mu.RLock()
	digest, found := memo.digests[value]
	memo.mu.RUnlock()
	if found {
		return digest
	}
	digest, err := contract.DeriveCanonicalDigestV2(memo.domain, value)
	if err != nil {
		return digest
	}
	memo.mu.Lock()
	if len(memo.digests) >= keySegmentMemoEntries {
		memo.digests = make(map[string]string)
	}
	memo.digests[value] = digest
	memo.mu.Unlock()
	return digest
}

func executionKey(prefix, kind string, plan execution.PlanIdentity, generation execution.StateGeneration, suffix string) string {
	tenant := tenantKeySegments.digest(plan.TenantID)
	generationDigest := generationKeySegments.digest(string(generation))
	parts := []string{prefix, kind, "v2", tenant[:32], plan.BusinessID, plan.StrategyID, generationDigest[:32]}
	if suffix != "" {
		parts = append(parts, suffix[:32])
	}
	return strings.Join(parts, ":")
}
