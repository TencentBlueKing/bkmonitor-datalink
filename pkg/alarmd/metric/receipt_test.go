// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestRecordMessageReceiptPreservesBusinessCountUnits(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	receipt, err := contract.BuildMessageReceiptV1(contract.MessageReceiptV1{
		ExecutionID: "execution-1", MessageID: "message-1",
		PayloadDigest: strings.Repeat("1", 64), PlanSetDigest: strings.Repeat("2", 64),
		SourceWindow: contract.SourceWindowV2{FromTime: 1, UntilTime: 2},
		Status:       contract.ReceiptStatusCompletedWithTerminal,
		Counts: contract.ReceiptCountsV1{
			Received: 3, Selected: 5, Processed: 3, Unavailable: 1, Terminal: 1,
			LevelTerminalAffected: 1, Events: 2,
		},
		PerPlan: []contract.PlanReceiptV1{
			{PlanID: "1001", Selected: 2, Normal: 1, Abnormal: 1, LevelTerminalAffected: 1},
			{PlanID: "1002", Selected: 2, Recovery: 1, Unavailable: 1},
			{PlanID: "1003", Selected: 1, Terminal: 1},
		},
	})
	if err != nil {
		t.Fatalf("BuildMessageReceiptV1() error = %v", err)
	}
	recorder.RecordValidatedMessageReceipt(receipt)

	got := scrape(t, recorder)
	for field, value := range map[string]string{
		"received": "3", "selected": "5", "processed": "3",
		"normal": "1", "abnormal": "1", "recovery": "1",
		"unavailable": "1", "terminal": "1", "events": "2",
		"level_terminal_affected": "1",
	} {
		want := `bkmonitor_alarmd_message_receipt_business_total{field="` + field + `"} ` + value
		if !strings.Contains(got, want) {
			t.Fatalf("metrics missing %q:\n%s", want, got)
		}
	}
	if want := `bkmonitor_alarmd_message_receipt_status_total{status="COMPLETED_WITH_TERMINAL"} 1`; !strings.Contains(got, want) {
		t.Fatalf("metrics missing %q:\n%s", want, got)
	}
}

func TestRecordMessageReceiptUsesOnlyContractStatuses(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	for _, receipt := range []*contract.MessageReceiptV1{
		buildMetricReceipt(t, contract.ReceiptStatusCompleted),
		buildMetricReceipt(t, contract.ReceiptStatusRejected),
	} {
		recorder.RecordValidatedMessageReceipt(receipt)
	}
	got := scrape(t, recorder)
	for _, status := range []string{contract.ReceiptStatusCompleted, contract.ReceiptStatusRejected} {
		want := `bkmonitor_alarmd_message_receipt_status_total{status="` + status + `"} 1`
		if !strings.Contains(got, want) {
			t.Fatalf("metrics missing %q:\n%s", want, got)
		}
	}
}

func TestRecordMessageReceiptDeliveryKeepsLifecycleOutcomesSeparate(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	recorder.RecordMessageReceiptQueued(3)
	recorder.RecordMessageReceiptACKed(2)
	recorder.RecordMessageReceiptDropped(1)

	got := scrape(t, recorder)
	for outcome, value := range map[string]string{"queued": "3", "acked": "2", "dropped": "1"} {
		want := `bkmonitor_alarmd_message_receipt_delivery_total{outcome="` + outcome + `"} ` + value
		if !strings.Contains(got, want) {
			t.Fatalf("metrics missing %q:\n%s", want, got)
		}
	}
}

func buildMetricReceipt(t *testing.T, status string) *contract.MessageReceiptV1 {
	t.Helper()
	receipt := contract.MessageReceiptV1{
		ExecutionID: "execution-1", MessageID: "message-1",
		PayloadDigest: strings.Repeat("1", 64), PlanSetDigest: strings.Repeat("2", 64),
		SourceWindow: contract.SourceWindowV2{FromTime: 1, UntilTime: 2}, Status: status,
	}
	if status == contract.ReceiptStatusRejected {
		receipt.ReasonCounts = []contract.ReasonCountV1{{ReasonCode: contract.ReasonMalformedJSON, Count: 1}}
	} else {
		receipt.Counts = contract.ReceiptCountsV1{Received: 1, Selected: 1, Processed: 1}
		receipt.PerPlan = []contract.PlanReceiptV1{{PlanID: "1001", Selected: 1, Normal: 1}}
	}
	built, err := contract.BuildMessageReceiptV1(receipt)
	if err != nil {
		t.Fatalf("BuildMessageReceiptV1() error = %v", err)
	}
	return built
}
