// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ownership_test

import (
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A REBALANCE Assignment is read like a RENDEZVOUS one, one release before
// anything writes it: a Leader that starts moving Query Groups must never
// publish a record a Worker of the previous release refuses. Any other
// reason stays refused, so the vocabulary is still closed.
func TestAssignmentReadersAcceptRebalanceBeforeAnyWriterExists(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	for _, reason := range []ownership.PlacementReason{ownership.PlacementRendezvous, ownership.PlacementRebalance} {
		record := ownership.AssignmentRecord{
			QueryGroup: "qg-1", DesiredWorkerID: "worker-1", AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1,
			PlacementReason: reason, AssignedAt: at,
		}
		if err := record.Validate(); err != nil {
			t.Fatalf("record with %s: %v", reason, err)
		}
		decision := ownership.AssignmentDecision{QueryGroup: "qg-1", DesiredWorkerID: "worker-1", PlacementReason: reason, DecidedAt: at}
		if err := decision.Validate(); err != nil {
			t.Fatalf("decision with %s: %v", reason, err)
		}
	}
	unknown := ownership.AssignmentRecord{
		QueryGroup: "qg-1", DesiredWorkerID: "worker-1", AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1,
		PlacementReason: "MANUAL", AssignedAt: at,
	}
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported placement reason") {
		t.Fatalf("an unknown placement reason was accepted: %v", err)
	}
	if err := (ownership.AssignmentDecision{QueryGroup: "qg-1", DesiredWorkerID: "worker-1", PlacementReason: "MANUAL", DecidedAt: at}).Validate(); err == nil {
		t.Fatal("an unknown placement reason was accepted on a decision")
	}
}
