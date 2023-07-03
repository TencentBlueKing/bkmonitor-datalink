// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beater_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/beater"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

// yaml文件格式
var yamlFile = `
type: ping
name: test
version: 1.2.3
dataid: 0
max_buffer_size: 10240
# 最大超时时间
max_timeout: 100s
# 最小检测间隔
min_period: 3s
tasks:
   - task_id: 5
     bk_biz_id: 1
     # 周期
     period: 60s
     # 超时
     timeout: 60s
     urls:
       - 127.0.0.1
       - www.baidu.com
     # 注入的labels
     labels:
        test1: ahaha
        label2: 12321`

// 测试通过字符串获取ucfg对象,判断标准是ucfg对象中能获取到指定数据
func TestParseConfig(t *testing.T) {
	// 获取configEngine
	ce := beater.NewBaseConfigEngine(context.Background()).(*beater.BaseConfigEngine)
	assert.NotNil(t, ce)

	// 反序列化yaml到ucfg对象
	cfg, err := ce.ParseToUcfg([]byte(yamlFile))
	assert.Nil(t, err)

	//校验操作是否成功
	name, err := cfg.String("name", 0)
	assert.Nil(t, err)
	minPeriod, err := cfg.String("min_period", 0)
	assert.Nil(t, err)
	maxTimeout, err := cfg.String("max_timeout", 0)
	assert.Nil(t, err)
	assert.Equal(t, "test", name)
	assert.Equal(t, "3s", minPeriod)
	assert.Equal(t, "100s", maxTimeout)

	// 手动创建一个metaconfig，从工厂获取会为空
	pingTaskMetaConfig := new(configs.PingTaskMetaConfig)

	// 填充config
	childTaskMetaConfig, err := ce.FillConfig(pingTaskMetaConfig, cfg, "/test")
	assert.Nil(t, err)

	// 校验参数是否正确填充
	assert.Equal(t, "test", childTaskMetaConfig.Name)
	assert.Equal(t, "ping", childTaskMetaConfig.Type)
	assert.Equal(t, "1.2.3", childTaskMetaConfig.Version)

}
