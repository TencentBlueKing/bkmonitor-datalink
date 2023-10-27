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
	"sync"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	t0 := time.Now()
	wg := sync.WaitGroup{}

	const worker = 10000
	for i := 0; i < worker; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fl := NewFlowLimiter("", 1024*1024*10)
			for j := 0; j < 500; j++ {
				fl.Consume(100 * 1024)
			}
		}()
	}

	wg.Wait()
	t.Log("total", time.Since(t0))
}
