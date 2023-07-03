// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package durationmeasurer

import (
	"context"
	"math/rand"
	"testing"
	"time"
)

func TestDurationMeasurer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dm := New(ctx, time.Second*1)

	// 正常情况下耗时在 10s 内
	for i := 0; i < 100000; i++ {
		f := float64(rand.Int31n(10))
		dm.Measure(time.Duration(f) * time.Second)
	}
	t.Logf("step1: P90=%v, P95=%v, P99=%v", dm.P90(), dm.P95(), dm.P99())

	// 出现了 100s 以上的耗时
	for i := 0; i < 10; i++ {
		f := float64(rand.Int31n(10) + 100)
		dm.Measure(time.Duration(f) * time.Second)
	}
	t.Logf("step2: P90=%v, P95=%v, P99=%v", dm.P90(), dm.P95(), dm.P99())

	// 重置 measurer
	time.Sleep(time.Second * 2)

	// 出现了 100s 以上的耗时
	for i := 0; i < 10; i++ {
		f := float64(rand.Int31n(10) + 100)
		dm.Measure(time.Duration(f) * time.Second)
	}
	t.Logf("step3: P90=%v, P95=%v, P99=%v", dm.P90(), dm.P95(), dm.P99())
}
