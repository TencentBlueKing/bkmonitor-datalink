// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution_test

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A carry is part of what an activation is: two activations that differ only
// in it are two activations, and one without it serializes as before.
func TestACarryIsPartOfTheActivation(t *testing.T) {
	plan := execution.ActivatedPlan{Identity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "7"},
		StateGeneration: "new", StateApplyEpoch: 2, ScheduleRevision: "s", RequiredFullSlots: 1, ForceWarming: true}
	carried := plan
	carried.Carry = &execution.StateCarry{From: "old", Levels: []uint32{1, 2}}
	if plan.Equal(carried) || carried.Equal(plan) {
		t.Fatal("an activation that differs only in its carry compared equal, and would not be rewritten")
	}
	other := carried
	other.Carry = &execution.StateCarry{From: "old", Levels: []uint32{1, 3}}
	if carried.Equal(other) {
		t.Fatal("two carries of different Levels compared equal")
	}
	same := carried
	same.Carry = &execution.StateCarry{From: "old", Levels: []uint32{1, 2}}
	if !carried.Equal(same) {
		t.Fatal("two carries decoded from the same bytes compared unequal")
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &keys); err != nil {
		t.Fatal(err)
	}
	if _, present := keys["Carry"]; present {
		t.Fatalf("an activation without a carry serializes one: %s", encoded)
	}
	for name, invalid := range map[string]execution.ActivatedPlan{
		"from its own generation": func() execution.ActivatedPlan {
			p := carried
			p.Carry = &execution.StateCarry{From: "new", Levels: []uint32{1}}
			return p
		}(),
		"on an activation that does not warm up": func() execution.ActivatedPlan { p := carried; p.ForceWarming = false; return p }(),
		"a Level twice": func() execution.ActivatedPlan {
			p := carried
			p.Carry = &execution.StateCarry{From: "old", Levels: []uint32{2, 2}}
			return p
		}(),
	} {
		if invalid.Carry.Validate(invalid) == nil {
			t.Errorf("a carry %s was accepted", name)
		}
	}
	if err := carried.Carry.Validate(carried); err != nil {
		t.Fatalf("a valid carry was refused: %v", err)
	}
	if !carried.CarriesLevel(2) || carried.CarriesLevel(3) || plan.CarriesLevel(1) {
		t.Fatal("CarriesLevel does not answer from the carry")
	}
}
