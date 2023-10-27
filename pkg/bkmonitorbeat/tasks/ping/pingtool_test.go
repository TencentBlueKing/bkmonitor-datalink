// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ping

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

func TestPingTool(t *testing.T) {
	// 生成初始化参数
	targetList := []*configs.Target{
		{
			Target:     "127.0.0.1",
			TargetType: "ip",
		},
		{
			Target:     "www.baidu.com",
			TargetType: "domain",
		},
	}

	DoPing = func(ctx context.Context, resMap map[string]map[string]*Info, t *BatchPingTool) map[string]map[string]*Info {
		for _, m := range resMap {
			for _, v := range m {
				v.RecvCount = 1
			}
		}
		return resMap
	}
	bt, err := NewBatchPingTool(context.Background(), InitTargets(targetList), 1, "3s", 56, 0, configs.IPAuto, configs.CheckModeAll, &tasks.NoopSemaphore{})
	assert.Nil(t, err)

	doFunc := func(resMap map[string]map[string]*Info, wg *sync.WaitGroup) {
		defer wg.Done()
		assert.Equal(t, len(targetList), len(resMap))
		for _, ipMap := range resMap {
			for _, v := range ipMap {
				assert.Equal(t, 1, v.RecvCount)
			}
		}
	}
	assert.Nil(t, bt.Ping(doFunc))
}
