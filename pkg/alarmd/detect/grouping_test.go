// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
)

func TestGroupSelectedRecordsOrdersSeriesAndRecords(t *testing.T) {
	records := []selectedRecord{
		{ordinal: 0, dimensionIdentityDigest: "series-b", sourceTime: 20, recordID: "b-20"},
		{ordinal: 1, dimensionIdentityDigest: "series-a", sourceTime: 20, recordID: "a-20-b"},
		{ordinal: 2, dimensionIdentityDigest: "series-a", sourceTime: 10, recordID: "a-10"},
		{ordinal: 3, dimensionIdentityDigest: "series-a", sourceTime: 20, recordID: "a-20-a"},
	}

	groups := groupSelectedRecords(records)
	if len(groups) != 2 || groups[0].dimensionIdentityDigest != "series-a" || groups[1].dimensionIdentityDigest != "series-b" {
		t.Fatalf("groupSelectedRecords() groups = %#v", groups)
	}
	wantOrdinals := []uint32{2, 3, 1}
	for index, want := range wantOrdinals {
		if groups[0].records[index].ordinal != want {
			t.Fatalf("series-a record %d ordinal = %d, want %d", index, groups[0].records[index].ordinal, want)
		}
	}
}

func TestGroupSelectedRecordsKeepsOrderedBacking(t *testing.T) {
	records := []selectedRecord{
		{ordinal: 1, dimensionIdentityDigest: "series", sourceTime: 10, recordID: "record-10"},
		{ordinal: 2, dimensionIdentityDigest: "series", sourceTime: 20, recordID: "record-20"},
	}

	groups := groupSelectedRecords(records)
	if groups[0].records[0].ordinal != 1 || groups[0].records[1].ordinal != 2 {
		t.Fatalf("ordered records changed: %#v", groups[0].records)
	}
}

func TestCollectSelectedRecordsUsesPlanViewWithoutCopyingDataset(t *testing.T) {
	plan := goldenPlanView(t)
	records, err := collectSelectedRecords(context.Background(), plan)
	if err != nil {
		t.Fatalf("collectSelectedRecords() error = %v", err)
	}
	if len(records) != 1 || records[0].ordinal != 0 || records[0].recordID == "" || records[0].view.RecordID() != records[0].recordID {
		t.Fatalf("collectSelectedRecords() = %#v", records)
	}
}

func TestCollectSelectedRecordsHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := collectSelectedRecords(ctx, goldenPlanView(t))
	if err != context.Canceled {
		t.Fatalf("collectSelectedRecords() error = %v, want context.Canceled", err)
	}
}

func goldenPlanView(t testing.TB) inputv2.PlanView {
	t.Helper()
	payload, err := os.ReadFile("../contract/testdata/go-v2/execution_envelope_v2.json")
	if err != nil {
		t.Fatalf("read v2 golden: %v", err)
	}
	result, err := inputv2.New(detectReaderLimits()).Decode(context.Background(), payload)
	if err != nil {
		t.Fatalf("decode v2 golden: %v", err)
	}
	plans := result.Input.PlanViews()
	if len(plans) != 1 {
		t.Fatalf("golden plan count = %d, want 1", len(plans))
	}
	return plans[0]
}

func detectReaderLimits() contract.ReaderLimitsV2 {
	return contract.ReaderLimitsV2{
		MaxEnvelopeBytes: 1 << 20, MaxRecordsPerMessage: 100, MaxPlansPerMessage: 10, MaxLevelsPerPlan: 10,
		MaxSelectorBytes: 1 << 16, MaxRecordBytes: 1 << 16, MaxPlanSetBytes: 1 << 18,
		MaxContractDepth: 32, MaxStringBytes: 1 << 16, MaxValidationIssues: 100,
	}
}
