// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metrictest"
)

const receiverRejectedName = "bk_collector_receiver_rejected_total"

// Every cell a rejection can be counted in is there, at 0, before any
// request comes in: the six reasons for HTTP and for gRPC, and the two
// reasons counted under a record's own request type, a missing pipeline and
// a partial token, for every other request type.
func TestReceiverRejectedCellsExistFromTheStart(t *testing.T) {
	var want []string
	for _, protocol := range []string{"http", "grpc"} {
		for _, reason := range []string{"too_large", "chunked_over_limit", "decompress", "decode", "token_partial", "no_pipeline"} {
			want = append(want, "protocol="+protocol+",reason="+reason)
		}
	}
	for _, protocol := range []string{"tars", "icmp", "derived"} {
		want = append(want, "protocol="+protocol+",reason=no_pipeline", "protocol="+protocol+",reason=token_partial")
	}
	sort.Strings(want)

	assert.Equal(t, want, metrictest.Series(t, receiverRejectedName))
	for _, protocol := range []string{"http", "grpc"} {
		v, ok := metrictest.Value(t, receiverRejectedName, map[string]string{"protocol": protocol, "reason": "too_large"})
		assert.True(t, ok)
		assert.Zero(t, v)
	}
}
