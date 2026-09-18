// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"linkd/internal/lifecycle/enrich"
)

// TestClient 在进程内模拟只读依赖；每次调用独立采样，不保存租户或告警状态。
// 使用标准库并发安全的随机函数，随机值只影响测试延迟和故障，不生成业务身份。
type TestClient struct{}

// Call 在独立超时内等待随机延迟，再按概率返回固定注入错误；父取消优先。
func (TestClient) Call(ctx context.Context, request enrich.TestRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Config.Validate(); err != nil {
		return err
	}
	if request.TenantID == "" || request.EventSourceID == "" || request.AlertID == "" || request.CallIndex < 0 || request.CallIndex >= request.Config.Calls {
		return fmt.Errorf("test datasource request requires stable identity and an in-range call index")
	}
	delay, fail := testSample(request.Config)
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(request.Config.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-callCtx.Done():
			return callCtx.Err()
		case <-timer.C:
		}
	}
	if err := callCtx.Err(); err != nil {
		return err
	}
	if fail {
		return enrich.ErrInjectedTestFailure
	}
	return nil
}

func testSample(c enrich.TestSourceConfig) (time.Duration, bool) {
	//nolint:gosec // G404: 随机数仅模拟测试延迟，不用于凭据、安全决策或业务身份。
	milliseconds := c.SleepMeanMilliseconds + c.SleepStddevMilliseconds*rand.NormFloat64()
	milliseconds = math.Max(0, math.Min(float64(c.SleepMaxMilliseconds), milliseconds))
	//nolint:gosec // G404: 概率只控制显式启用的测试故障注入。
	fail := rand.Float64() < c.ErrorRate
	return time.Duration(milliseconds * float64(time.Millisecond)), fail
}
