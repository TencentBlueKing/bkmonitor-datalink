// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Plan's memory is bounded by its own item's max_groups when the item
// states one, and by the deployment's otherwise; a refusal says which.
func TestTheNoDataGroupBoundIsThePlansOwnOrTheDeployments(t *testing.T) {
	groups := func(n int) []execution.NoDataGroupMemory {
		out := make([]execution.NoDataGroupMemory, 0, n)
		for index := 0; index < n; index++ {
			out = append(out, execution.NoDataGroupMemory{GroupKey: fmt.Sprintf("data_set_id=%d", index), LastSeen: 940, FirstAbsent: 980})
		}
		return out
	}
	for name, test := range map[string]struct {
		global, own, groups int
		refused             bool
		limit               int
		source              string
	}{
		"over the deployment's bound":           {global: 3, groups: 4, refused: true, limit: 3, source: execution.NoDataLimitPlatform},
		"at the deployment's bound":             {global: 3, groups: 3},
		"over the item's own, under the global": {global: 5, own: 2, groups: 3, refused: true, limit: 2, source: execution.NoDataLimitStrategy},
		"over the global, under the item's own": {global: 3, own: 6, groups: 5},
	} {
		t.Run(name, func(t *testing.T) {
			store := boundedGroupStore(t, &casMemoryBackend{values: make(map[string][]byte)}, test.global)
			mutation := noDataMutationV2(t, 0, groups(test.groups)...)
			mutation.GroupLimit = uint32(test.own)
			applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{Retention: execution.GenerationRetention{Unknown: true},
				Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation}})
			if err != nil {
				t.Fatal(err)
			}
			item := applied.Items[0]
			if !test.refused {
				if item.Status != execution.NoDataApplied {
					t.Fatalf("apply = %+v, want the memory stored", item)
				}
				return
			}
			if item.Status != execution.NoDataRejected || item.ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) ||
				item.Size == nil || item.Size.Groups != test.groups || item.Size.Limit != test.limit || item.Size.LimitSource != test.source {
				t.Fatalf("apply = %+v size %+v, want refused with %d groups against %d from %s", item, item.Size, test.groups, test.limit, test.source)
			}
		})
	}
}
