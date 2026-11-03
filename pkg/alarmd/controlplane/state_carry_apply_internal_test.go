// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A decided carry is put on its record, with one full Slot of warming, unless
// the record's Query Group is back from retirement: then it carries nothing
// and keeps the warming its whole window needs.
func TestAReturningQueryGroupCarriesNothingAcrossItsHole(t *testing.T) {
	record := func() PlanActivationRecord {
		return PlanActivationRecord{Fact: execution.PlanActivationFact{Selected: execution.ActivatedPlan{
			StateGeneration: "new", RequiredFullSlots: 5, ForceWarming: true}}}
	}
	records := []PlanActivationRecord{record(), record(), record()}
	carry := &execution.StateCarry{From: "old", Levels: []uint32{1}}
	carried, discontinuous := applyStateCarries(records,
		map[int]*execution.StateCarry{0: carry, 1: carry}, map[int]struct{}{1: {}})
	if carried != 1 || discontinuous != 1 {
		t.Fatalf("carried=%d discontinuous=%d, want one of each", carried, discontinuous)
	}
	if got := records[0].Fact.Selected; got.Carry != carry || got.RequiredFullSlots != 1 {
		t.Fatalf("carried record = %+v, want the carry and one full Slot", got)
	}
	for _, index := range []int{1, 2} {
		if got := records[index].Fact.Selected; got.Carry != nil || got.RequiredFullSlots != 5 {
			t.Fatalf("record %d = %+v, want no carry and its whole window", index, got)
		}
	}
}
