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
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestPingGatherRun(t *testing.T) {
	globalConf := configs.NewConfig()
	// 提供一个心跳的data_id，防止命中data_id防御机制
	globalConf.HeartBeat.GlobalDataID = 1000
	taskConf := configs.NewPingTaskConfig()
	targetList := []*configs.Target{
		{
			Target:     "127.0.0.1",
			TargetType: "ip",
		},
		{
			Target:     "www.baidu.com",
			TargetType: "domain",
		},
		{
			Target:     "1.1.1.1",
			TargetType: "ip",
		},
		{
			Target:     "2.2.2",
			TargetType: "ip",
		},
		{
			Target:     "www.a.com",
			TargetType: "domain",
		},
	}
	taskConf.TotalNum = 1
	taskConf.MaxRTT = "3s"
	taskConf.BatchSize = 0
	taskConf.Targets = targetList
	taskConf.PingSize = 56
	taskConf.TargetIPType = configs.IPAuto
	taskConf.DNSCheckMode = configs.CheckModeAll
	taskConf.Timeout = 3 * time.Second

	gather := New(globalConf, taskConf)

	e := make(chan define.Event, 100)
	gather.Run(context.Background(), e)
	gather.Wait()
	close(e)
	for ev := range e {
		event := ev.AsMapStr()
		t.Logf("Event: %v \n", event)
		if _, ok := event["dimension"]; ok {
			dims := event["dimension"].(common.MapStr)
			target := dims["bk_target_ip"].(string)
			bkmUpCode := dims["bkm_up_code"].(string)
			if target == "www.a.com" {
				assert.Equal(t, bkmUpCode, "2101")
			}
			if target == "2.2.2" {
				assert.Equal(t, bkmUpCode, "2102")
			}
			if target == "1.1.1.1" {
				assert.Equal(t, bkmUpCode, "0")
			}
		}
	}
}
