// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// These durations nest: RunOne includes preparation and execution. They are not
// additive CPU time and do not measure time queued before RunOne is called.
func startSlotTiming(ctx context.Context, observer observability.Observer, stage observability.Stage, now func() time.Time) func() {
	if observer == nil {
		return func() {}
	}
	started := now()
	return func() {
		// This records how long the call took, nothing else. It used to stamp
		// every one of them TERMINAL, which in this catalog is a failure result,
		// and with no reason - so it normalized to "nobody reported one" and the
		// object page showed round after round of a Query Group terminating for
		// an unstated cause. The Query Group was fine: the overwhelming majority
		// of these calls are a dispatcher poll answering "not due yet", which is
		// the normal shape of a tick and about nine in ten of them.
		//
		// The failures on this path report themselves, with their own reasons,
		// where they happen. A duration record has no outcome to add.
		observeRuntime(ctx, observer, observability.Observation{
			Component: observability.ComponentScheduler, Stage: stage,
			Result: observability.ResultSuccess, Direction: observability.DirectionInternal,
			Duration: now().Sub(started),
		})
	}
}
