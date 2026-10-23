// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A contract with no read hold encodes as a contract always has, byte for
// byte: the field is omitted at zero, so records, digests and stream headers
// written before it existed, and every Slot never held, are unchanged. One
// with a hold carries it.
func TestAContractWithNoReadHoldEncodesAsBefore(t *testing.T) {
	ref := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "qg", EvaluationTime: 1_788_000_000},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 1_787_999_940, DuePlanSetDigest: "digest-v1",
	}
	const before = `{"Slot":{"QueryGroup":"qg","EvaluationTime":1788000000},"SnapshotRevision":"snapshot-v1",` +
		`"QueryRevision":"query-v1","ScheduleRevision":"schedule-v1","ScheduleSegmentStart":1787999940,"DuePlanSetDigest":"digest-v1"}`
	encoded, err := json.Marshal(ref)
	if err != nil || string(encoded) != before {
		t.Fatalf("encoded %s %v, want the bytes a contract without a read hold always had:\n%s", encoded, err, before)
	}
	ref.ReadHoldMillis = 60_000
	held, err := json.Marshal(ref)
	if err != nil || !strings.HasSuffix(string(held), `"DuePlanSetDigest":"digest-v1","ReadHoldMillis":60000}`) {
		t.Fatalf("encoded %s %v, want the hold carried", held, err)
	}
	ref.ReadHoldMillis = -1
	if ref.Validate() == nil {
		t.Fatal("a negative read hold validated")
	}
}

// A contract frozen with a read hold has its due Plans' deadlines that much
// later, and a fact is valid only for the hold it was requested with: the
// same Slot frozen with another hold is another contract.
func TestAFrozenFactCarriesTheReadHoldItWasRequestedWith(t *testing.T) {
	fact, request := exactDueRefsFact(t)
	if err := fact.Validate(request); err != nil {
		t.Fatalf("the unheld fact does not validate: %v", err)
	}
	spec := fact.DuePlans[0].ScheduleSpec
	held, ok := spec.HeldCompletionDeadlineUnixMilli(request.EvaluationTime, 60_000)
	unheld, _ := spec.CompletionDeadlineUnixMilli(request.EvaluationTime)
	if !ok || held != unheld+60_000 {
		t.Fatalf("held deadline %d, want the schedule's %d a minute later", held, unheld)
	}
	if _, ok := spec.HeldCompletionDeadlineUnixMilli(request.EvaluationTime, -1); ok {
		t.Fatal("a negative read hold gave a deadline")
	}

	request.ReadHoldMillis = 60_000
	if fact.Validate(request) == nil {
		t.Fatal("a fact frozen with no hold validated against a request with one")
	}
	fact.Contract.ReadHoldMillis = 60_000
	if fact.Validate(request) == nil {
		t.Fatal("a fact whose deadlines do not carry its hold validated")
	}
	fact.DuePlans[0].CompletionDeadlineUnixMilli = held
	digest, err := execution.DeriveDuePlanSetDigest(fact.DuePlans, fact.Requirements)
	if err != nil {
		t.Fatalf("DeriveDuePlanSetDigest() error = %v", err)
	}
	if digest == fact.Contract.DuePlanSetDigest {
		t.Fatal("the held deadline left the due Plan set digest as it was")
	}
	fact.Contract.DuePlanSetDigest = digest
	if err := fact.Validate(request); err != nil {
		t.Fatalf("a fact frozen with its hold does not validate: %v", err)
	}
}
