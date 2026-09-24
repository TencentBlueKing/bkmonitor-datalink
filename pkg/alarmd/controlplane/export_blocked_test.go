// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import "context"

// ForgetActivationCacheForTest drops the parsed activation, so a test that
// rewrites the body under an unchanged header reads what it wrote.
func (repository *RedisCatalogRepository) ForgetActivationCacheForTest() {
	repository.activationCache.mu.Lock()
	repository.activationCache.entry = nil
	repository.activationCache.mu.Unlock()
}

// EncodeBlockedSetForTest and BlockedDigestForTest are the persisted form of
// a held-back set and the digest the activation body counts it by.
func EncodeBlockedSetForTest(groups []BlockedQueryGroup) ([]byte, error) {
	return encodeBlockedSet(groups)
}
func BlockedDigestForTest(groups []BlockedQueryGroup) string { return blockedDigest(groups) }

// PersistActivationRefUpgradeForTest drives the v1-to-v2 ref upgrade's write,
// which no reachable fixture takes any more.
func (repository *RedisCatalogRepository) PersistActivationRefUpgradeForTest(
	ctx context.Context, expected ActivationExpectation, next ActivationState, activePayload []byte,
) error {
	return repository.persistActivationRefUpgrade(ctx, expected, next, activePayload)
}

// CutoverScriptForTest is the cutover script's text.
func CutoverScriptForTest() string { return compareAndSetCutoverSchedulesScript }
