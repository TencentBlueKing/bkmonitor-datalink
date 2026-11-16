// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// A takeover that took its lease is written each time, with the lease's
// epochs: two handovers of one Query Group within the hour are two lines,
// told apart by the epochs, where the hourly sample kept one. A refused one
// names the holder it ran into, or why it cannot.
func TestATakeoverIsWrittenEachTimeAndARefusalNamesItsHolder(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	now := time.Unix(1_000, 0)
	observer := levelObserver(t, &output, &now)
	ctx := context.Background()
	for _, epoch := range []uint64{3, 4} {
		observer.Observe(ctx, Observation{Component: ComponentOwnership, Stage: StageTakeoverCompleted, Result: ResultSuccess,
			Trace: TraceFields{QueryGroupKey: "qg-a", OwnerEpoch: epoch, SessionEpoch: 7}})
	}
	busy := errors.New("alarmd ownership: lease is held by another owner")
	observer.Observe(ctx, Observation{Component: ComponentOwnership, Stage: StageTakeoverCompleted, Result: ResultFailed,
		ReasonCode: "LEASE_BUSY", Trace: TraceFields{QueryGroupKey: "qg-b"}, Err: busy,
		LeaseHolder: &LeaseHolderFacts{OwnerID: "worker-2", OwnerEpoch: 9, SessionEpoch: 11}})
	observer.Observe(ctx, Observation{Component: ComponentOwnership, Stage: StageTakeoverCompleted, Result: ResultFailed,
		ReasonCode: "LEASE_BUSY", Trace: TraceFields{QueryGroupKey: "qg-c"}, Err: busy,
		LeaseHolder: &LeaseHolderFacts{Unknown: LeaseHolderNoWorkerSession}})

	lines := decodeLines(t, &output)
	if len(lines) != 4 {
		t.Fatalf("lines=%v, want both takeovers of qg-a and both refusals", lines)
	}
	for index, epoch := range []float64{3, 4} {
		line := lines[index]
		if line["stage"] != string(StageTakeoverCompleted) || line["query_group_key"] != "qg-a" ||
			line["owner_epoch"] != epoch || line["session_epoch"] != float64(7) {
			t.Fatalf("takeover line %d = %v, want qg-a under owner epoch %v and session epoch 7", index, line, epoch)
		}
	}
	if line := lines[2]; line["query_group_key"] != "qg-b" || line["holder_owner_id"] != "worker-2" ||
		line["holder_owner_epoch"] != float64(9) || line["holder_session_epoch"] != float64(11) || line["holder_unknown"] != nil {
		t.Fatalf("refusal with a holder = %v, want worker-2 under owner epoch 9 and session epoch 11", line)
	}
	if line := lines[3]; line["query_group_key"] != "qg-c" || line["holder_unknown"] != LeaseHolderNoWorkerSession ||
		line["holder_owner_id"] != nil {
		t.Fatalf("refusal without a holder = %v, want holder_unknown %s and no holder", line, LeaseHolderNoWorkerSession)
	}
}
