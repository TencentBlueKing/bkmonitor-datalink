// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"fmt"
	"testing"
)

func BenchmarkRawBatchPack(b *testing.B) {
	for _, memberCount := range []int{5, 20, 50, 200} {
		b.Run(fmt.Sprintf("members_%d", memberCount), func(b *testing.B) {
			members := make([]RawBatchMember, memberCount)
			for index := range members {
				members[index] = RawBatchMember{
					Ordinal: index,
					Prepared: &PreparedRawQuery{
						queryOption: &queryOption{
							indexes: []string{fmt.Sprintf("trace-index-%03d", index)},
						},
						body: `{"from":0,"size":100,"query":{"bool":{"filter":[{"term":{"trace_id":"trace-sentinel"}}]}}}`,
					},
				}
			}

			b.ReportAllocs()
			b.SetBytes(int64(memberCount))
			b.ResetTimer()
			for range b.N {
				batches, oversized, err := PackRawBatchMembers(members, 16, 1<<20)
				if err != nil {
					b.Fatal(err)
				}
				if len(oversized) != 0 || len(batches) == 0 {
					b.Fatalf("unexpected packing result: batches=%d oversized=%d", len(batches), len(oversized))
				}
			}
		})
	}
}
