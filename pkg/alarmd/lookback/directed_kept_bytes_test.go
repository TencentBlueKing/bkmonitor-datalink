// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A directed read keeps a late series at what its Dataset holds, not at the
// bytes the answer encoded it in: the memory line is asked for the
// Dataset's RetainedBytes, and the read's byte count is the encoded length.
func TestADirectedReadKeepsASeriesAtWhatItsDatasetHolds(t *testing.T) {
	var asks []uint64
	sink := &keptSink{first: map[uint64]struct{}{}, admit: func(bytes uint64) bool { asks = append(asks, bytes); return true }}
	series := dataset("h1", map[int64]string{60: "1", 120: "2"})
	const wire = 7
	if err := sink.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{Dataset: series,
		Delivery: execution.SeriesDelivery{PhysicalQuery: "q", QueryRevision: "r", Series: 1, Records: 2, Bytes: wire, Digest: "d"}}); err != nil {
		t.Fatal(err)
	}
	if len(asks) != 1 || asks[0] != series.RetainedBytes() || sink.keptBytes != series.RetainedBytes() || sink.bytes != wire {
		t.Fatalf("asked %v, kept %d, read %d; want the Dataset's %d asked and kept and the %d encoded bytes read",
			asks, sink.keptBytes, sink.bytes, series.RetainedBytes(), wire)
	}
}
