// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

func BenchmarkAcquire(b *testing.B) {
	type args struct {
		weight int64
		n      int64
		times  int
	}
	tests := []struct {
		name string
		args args
	}{
		{"总数", args{1, 1, 1}},
		{"总数", args{2, 1, 1}},
		{"总数", args{128, 1, 1}},
		{"单次", args{2, 2, 1}},
		{"单次", args{16, 16, 1}},
		{"单次", args{128, 128, 1}},
		{"循环次数", args{2, 1, 2}},
		{"循环次数", args{16, 1, 16}},
		{"循环次数", args{128, 1, 128}},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("%s 总数%d 单次%d 循环%d次", tt.name, tt.args.weight, tt.args.n, tt.args.times)
		b.Run(name, func(b *testing.B) {
			p := tasks.NewSemaphorePool()
			s := p.GetSemaphore("k1", tt.args.weight, "k2", tt.args.weight)
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < tt.args.times; j++ {
					err := s.Acquire(ctx, tt.args.n)
					if err != nil {
						b.Fatalf("Acquired error: %v", err)
						return
					}
				}
				for j := 0; j < tt.args.times; j++ {
					s.Release(tt.args.n)
				}
			}
		})
	}
}
