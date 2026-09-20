// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

// Deriving the budget and installing it are separate steps, and only the second
// one changes how the process runs. The setters are injected so that proving
// the process configures itself does not mean changing the collector for every
// other test in this binary.
func TestGoRuntimeBudgetIsInstalledExactlyWhenItWasDerived(t *testing.T) {
	type applied struct {
		limits   []int64
		percents []int
	}
	install := func(derived config.DerivedGoRuntime) applied {
		var got applied
		applyPhaseTwoGoRuntime(derived, goRuntimeBudgetSetters{
			setMemoryLimit: func(limit int64) int64 { got.limits = append(got.limits, limit); return 0 },
			setGCPercent:   func(percent int) int { got.percents = append(got.percents, percent); return 0 },
		})
		return got
	}

	container := config.DeriveGoRuntime(config.CapacityInputs{
		CPUBudget: 8, MemoryLimitBytes: 8 << 30, MemorySource: "pod_limit",
	})
	if got := install(container); len(got.limits) != 1 || got.limits[0] != container.MemoryLimitBytes ||
		len(got.percents) != 1 || got.percents[0] != container.GCPercent {
		t.Fatalf("installed %+v, want the derived %d bytes / %d percent",
			got, container.MemoryLimitBytes, container.GCPercent)
	}
	// A limit nobody stated must reach the runtime as nothing at all, not as
	// the fallback number dressed up as a container's.
	if got := install(config.DerivedGoRuntime{MemoryLimitBytes: 1 << 30, GCPercent: 300}); len(got.limits) != 0 ||
		len(got.percents) != 0 {
		t.Fatalf("installed %+v from a budget that was never derived, want nothing", got)
	}
}
