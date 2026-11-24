// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// slot.query names, beside its re-query, the Plans whose activation carried
// history across a detection edit and is in force at the Slot, from the read
// object.get's plan_carries is: a point before activated_at was decided under
// carried_from. A Plan that carried nothing, or whose
// activation came after the Slot, is not named, and a read that failed says
// why.
func TestSlotQueryNamesThePlansThatCarriedIntoTheSlot(t *testing.T) {
	plan := slotFixture(t)
	slot := plan.Contract.Slot.EvaluationTime
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["fixture-host"],"values":[[1700000000000,42]]}],"is_partial":false}`))
	}))
	defer server.Close()
	client, err := uq.NewDiagnosticClient(server.URL, "fixture", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	carried := controlplane.PlanCarryFact{Plan: execution.PlanIdentity{StrategyID: "324"}, StateGeneration: "after", ActivatedAt: slot - 60, ActivationFound: true, StrategyRevision: 2,
		CarriedFrom: &controlplane.PlanCarrySource{StateGeneration: "before", Levels: []uint32{2}, Found: true, StrategyRevision: 1}}
	uncarried := controlplane.PlanCarryFact{Plan: execution.PlanIdentity{StrategyID: "325"}, StateGeneration: "same", ActivatedAt: slot - 600, ActivationFound: true}
	later := carried
	later.Plan, later.ActivatedAt = execution.PlanIdentity{StrategyID: "326"}, slot+60
	for _, tc := range []struct {
		name        string
		plans       []controlplane.PlanCarryFact
		err         error
		want        []string
		unavailable string
	}{
		{name: "carried", plans: []controlplane.PlanCarryFact{carried, uncarried, later}, want: []string{"324"}},
		{name: "nothing carried", plans: []controlplane.PlanCarryFact{uncarried}},
		{name: "carried after the slot", plans: []controlplane.PlanCarryFact{later}},
		{name: "no open segment", err: fmt.Errorf("read: %w", controlplane.ErrScheduleUnavailable), unavailable: "no_open_segment"},
		{name: "read failed", err: errors.New("redis down"), unavailable: "read_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked execution.QueryGroupIdentity
			ops := SlotOperations(SlotOptions{Resolve: func(context.Context, execution.SlotIdentity) (SlotPlan, error) { return plan, nil }, UQ: client,
				PlanCarries: func(_ context.Context, group execution.QueryGroupIdentity) (controlplane.PlanCarriesReading, error) {
					asked = group
					return controlplane.PlanCarriesReading{QueryGroup: group, Plans: tc.plans, Total: len(tc.plans)}, tc.err
				}})
			preview := ops[0].Run(context.Background(), jsonParams(t, slotParams(slotContext(plan))))
			out := ops[1].Run(context.Background(), jsonParams(t, preview.Next[0].Params))
			if out.Error != nil || asked != plan.Contract.Slot.QueryGroup {
				t.Fatalf("out=%+v asked %q", out.Error, asked)
			}
			raw, err := json.Marshal(out.Value)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				PlanCarries []struct {
					Plan        execution.PlanIdentity    `json:"plan"`
					ActivatedAt *execution.EvaluationTime `json:"activated_at"`
					CarriedFrom *struct {
						StateGeneration  execution.StateGeneration `json:"state_generation"`
						Levels           []uint32                  `json:"levels"`
						StrategyRevision int64                     `json:"strategy_revision"`
					} `json:"carried_from"`
				} `json:"plan_carries"`
				Unavailable string `json:"plan_carries_unavailable"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if body.Unavailable != tc.unavailable || len(body.PlanCarries) != len(tc.want) {
				t.Fatalf("slot.query says %s, want Plans %v, unavailable %q", raw, tc.want, tc.unavailable)
			}
			if len(tc.want) == 0 && strings.Contains(string(raw), `"plan_carries"`) {
				t.Fatalf("slot.query names plan_carries when none carried into the Slot: %s", raw)
			}
			for index, id := range tc.want {
				got := body.PlanCarries[index]
				if got.Plan.StrategyID != id || got.ActivatedAt == nil || *got.ActivatedAt != carried.ActivatedAt ||
					got.CarriedFrom == nil || got.CarriedFrom.StateGeneration != "before" || len(got.CarriedFrom.Levels) != 1 || got.CarriedFrom.Levels[0] != 2 || got.CarriedFrom.StrategyRevision != 1 {
					t.Fatalf("slot.query names %s, want strategy %s activated at %d carried from generation before, Levels [2], revision 1", raw, id, carried.ActivatedAt)
				}
			}
		})
	}
	// Without the read bound, slot.query says nothing of carries.
	ops := SlotOperations(SlotOptions{Resolve: func(context.Context, execution.SlotIdentity) (SlotPlan, error) { return plan, nil }, UQ: client})
	preview := ops[0].Run(context.Background(), jsonParams(t, slotParams(slotContext(plan))))
	raw, _ := json.Marshal(ops[1].Run(context.Background(), jsonParams(t, preview.Next[0].Params)).Value)
	if strings.Contains(string(raw), "plan_carries") {
		t.Fatalf("slot.query without the read names carries: %s", raw)
	}
}
