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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// TestParamClean 测试清洗后的结果是否符合预期
func TestParamClean(t *testing.T) {
	param := configs.NewBaseTaskParam()
	assert.Nil(t, param.CleanParams())
	assert.Equal(t, define.DefaultTimeout, param.Timeout)
	assert.Equal(t, define.DefaultTimeout, param.AvailableDuration)
	assert.Equal(t, define.DefaultPeriod, param.Period)
}

// TestMetaParamClean 测试清洗后的结果是否符合预期
func TestMetaParamClean(t *testing.T) {
	param := configs.NewBaseTaskMetaParam()
	assert.Nil(t, param.CleanParams())
	assert.Equal(t, define.DefaultTimeout, param.MaxTimeout)
	assert.Equal(t, define.DefaultPeriod, param.MinPeriod)
}
