// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The group bound compilation froze into a Plan from its item's max_groups
// is the one the Plan's memory write carries to the store; a Plan without
// one carries none, so the deployment's applies, and encodes as it did
// before the field existed.
func TestAPlansFrozenGroupBoundReachesItsMemoryWrite(t *testing.T) {
	for name, test := range map[string]struct {
		maxGroups uint32
	}{"without one": {}, "with its own": {maxGroups: 7}} {
		t.Run(name, func(t *testing.T) {
			config := &contract.NoDataConfigV1{Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"},
				MaxGroups: test.maxGroups}
			due := noDataWiredPlan(t)
			due.CompiledPlan = noDataPreflightPlan(t, "7", config)
			stream := noDataWiredStream(t, due, &emptyNoDataStore{})
			if err := stream.loadNoDataMemory(context.Background()); err != nil {
				t.Fatal(err)
			}
			round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
			if err != nil || round.mutation == nil {
				t.Fatalf("round = %+v, %v; want a memory to store", round, err)
			}
			if round.mutation.GroupLimit != test.maxGroups {
				t.Fatalf("the memory write carries bound %d, want the Plan's %d", round.mutation.GroupLimit, test.maxGroups)
			}
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if stated := strings.Contains(string(encoded), "no_data_max_groups"); stated != (test.maxGroups > 0) {
				t.Fatalf("the frozen config encodes as %s; the field is there only when the item stated a bound", encoded)
			}
		})
	}
}
