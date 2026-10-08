// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Plan refused at admission for its lifetime names the rule and carries the
// store's sentence with both numbers: the lifetime it needs and the ceiling.
// It carried STATE_BUDGET_EXCEEDED and nothing else, and a sixty-hour
// strategy refused at every round read the same as a record past the value
// limit. Both ways past the ceiling: a retention span with no horizon, and a
// horizon cap one second over.
func TestARefusedLifetimeNamesItsRuleAndBothNumbers(t *testing.T) {
	retention := ttlTestRetention(120, time.Minute, 0)
	// What the span needs before any rounding: 120 one-minute points and
	// newBatchStore's one-minute restart margin, against its one-hour ceiling.
	required := 120*time.Minute + time.Minute
	for _, arm := range []struct {
		name    string
		horizon int64
		numbers []string
	}{
		{name: "a span past the ceiling", numbers: []string{required.String(), time.Hour.String()}},
		{name: "a horizon cap past the ceiling", horizon: 3601, numbers: []string{(3601 * time.Second).String(), time.Hour.String()}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			store := newBatchStore(t, newTTLRecordingBackend(), nil)
			request := execution.StateApplyRequest{Contract: frozenRef(), Retention: retention,
				Items: seriesMutations(t, 2, applyVersion(), 0), HorizonSeconds: arm.horizon}
			admission, err := store.AdmitRuntime(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			for index, item := range admission.Items {
				if item.ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) || item.RefusalRule != RuntimeRuleLifetimePastCeiling {
					t.Fatalf("admission item %d = %+v, want STATE_BUDGET_EXCEEDED under %s", index, item, RuntimeRuleLifetimePastCeiling)
				}
				for _, number := range arm.numbers {
					if !strings.Contains(item.RefusalText, number) {
						t.Fatalf("admission item %d says %q, want it to name %s", index, item.RefusalText, number)
					}
				}
			}
			applied, err := store.ApplyRuntime(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			for index, item := range applied.Items {
				if item.RefusalRule != RuntimeRuleLifetimePastCeiling {
					t.Fatalf("apply item %d = %+v, want it under %s as admission put it", index, item, RuntimeRuleLifetimePastCeiling)
				}
			}
		})
	}
}

// A record past the value limit is the other STATE_BUDGET_EXCEEDED, and says
// so: its own rule, and the bytes it encodes to against the limit. At the
// limit it is admitted.
func TestARecordPastTheValueLimitNamesItsRuleAndBothSizes(t *testing.T) {
	mutation := seriesMutations(t, 1, applyVersion(), 0)[0]
	encoded, _, err := encodeRuntimePackedCounted(mutation, mutation.ExpectedBlobRevision+1)
	if err != nil {
		t.Fatal(err)
	}
	size := len(encoded)
	for _, arm := range []struct {
		name    string
		limit   int
		refused bool
	}{
		{name: "at the limit", limit: size},
		{name: "a byte over it", limit: size - 1, refused: true},
	} {
		t.Run(arm.name, func(t *testing.T) {
			router, err := NewFixedRouter("state-01", newTTLRecordingBackend())
			if err != nil {
				t.Fatal(err)
			}
			store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: arm.limit,
				MaxItemsPerCall: 8, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			admission, err := store.AdmitRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(),
				Retention: testRetention(), Items: []execution.StateMutation{mutation}})
			if err != nil {
				t.Fatal(err)
			}
			item := admission.Items[0]
			if !arm.refused {
				if item.Status != execution.StateAdmissionAccepted || item.RefusalRule != "" || item.RefusalText != "" {
					t.Fatalf("admission = %+v, want admitted with no refusal words", item)
				}
				return
			}
			if item.ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) || item.RefusalRule != RuntimeRuleValueOverLimit {
				t.Fatalf("admission = %+v, want STATE_BUDGET_EXCEEDED under %s", item, RuntimeRuleValueOverLimit)
			}
			for _, number := range []int{size, arm.limit} {
				if !strings.Contains(item.RefusalText, " "+strconv.Itoa(number)+" bytes") {
					t.Fatalf("admission says %q, want it to name %d bytes", item.RefusalText, number)
				}
			}
		})
	}
}
