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
