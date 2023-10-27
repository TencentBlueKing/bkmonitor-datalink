// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs_test

import (
	"math/rand"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestConfigWithTcpConf(t *testing.T) {
	conf := configs.NewConfig()
	// 提供一个心跳的data_id，防止命中data_id防御机制
	conf.HeartBeat.GlobalDataID = 1000
	var globalConf define.Config = conf

	conf.TCPTask.DataID = int32(rand.Int31())

	taskConf := configs.NewTCPTaskConfig()
	conf.TCPTask.Tasks = append(conf.TCPTask.Tasks, taskConf)

	err := globalConf.Clean()
	if err != nil {
		t.Error(err.Error())
	}

	if taskConf.DataID != conf.TCPTask.DataID {
		t.Errorf("config clean error: dataid is %v, not %v", taskConf.DataID, conf.TCPTask.DataID)
	}

	taskConfigList := globalConf.GetTaskConfigListByType(configs.ConfigTypeTCP)
	if len(taskConfigList) != 1 {
		t.Errorf("get task config list by type %v error", configs.ConfigTypeTCP)
	}
	if taskConfigList[0].GetIdent() != taskConf.GetIdent() {
		t.Errorf("get task config from task list fail")
	}
}
