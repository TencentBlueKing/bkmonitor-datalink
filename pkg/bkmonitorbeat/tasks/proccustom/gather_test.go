// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proccustom

import (
	"context"
	"fmt"
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestProcCustomGatherRun(t *testing.T) {
	globalConfig := configs.NewConfig()
	taskConf := configs.NewProcCustomConfig(globalConfig)
	taskConf.DataID = 1
	taskConf.PIDPath = "/aaa/aaaa.pid"
	taskConf.MatchPattern = "polkitd"
	taskConf.Labels = []map[string]string{
		{"bk_target_cloud_id": "0", "bk_target_ip": "127.0.0.1"},
	}
	gather := New(globalConfig, taskConf)
	e := make(chan define.Event, 100)
	gather.Run(context.Background(), e)
	gather.Wait()
	close(e)
	for ev := range e {
		event := ev.AsMapStr()
		fmt.Printf("Event: %v \n", event)
		dimension := event["dimensions"].(common.MapStr)
		assert.Equal(t, dimension["bkm_up_code"].(string), "2401")
	}
}
