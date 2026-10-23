// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

// ForgetReusableRoundForTest drops the round the next one could stand on, so
// a test can have the same inputs rebuilt and compare the two.
func (reconciler *SourceReconciler) ForgetReusableRoundForTest() {
	reconciler.reusable = nil
}

// HeldTermForTest says which of the things a Leader keeps between rounds
// this reconciler holds: the observation, the last-good publication, the
// reusable round, and how many compiled candidates.
func (reconciler *SourceReconciler) HeldTermForTest() (observation, lastGood, reusable bool, candidates int) {
	return reconciler.memory.Load() != nil, reconciler.lastGood != nil, reconciler.reusable != nil, reconciler.candidates.Len()
}
