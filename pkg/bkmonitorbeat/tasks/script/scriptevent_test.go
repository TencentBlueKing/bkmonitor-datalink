// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package script

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestScriptEvent(t *testing.T) {
	globalConf := configs.NewConfig()
	// 提供一个心跳的data_id，防止命中data_id防御机制
	globalConf.HeartBeat.GlobalDataID = 1000
	taskConf := configs.NewScriptTaskConfig()
	err := globalConf.Clean()
	if err != nil {
		t.Errorf(err.Error())
	}
	err = taskConf.Clean()
	if err != nil {
		t.Errorf(err.Error())
	}
	st := New(globalConf, taskConf).(*Gather)
	event := NewEvent(st)
	if event.ErrorCode != define.CodeUnknown {
		t.Errorf("script event initial failed")
	}
}
