// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"os"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

const (
	Test = "test"
)

// TestInitConfig 加载测试配置
func TestInitConfig(t *testing.T) {
	// CustomConfigFilePath is exists
	CustomConfigFilePath = "../unify-query.yaml"
	InitConfig()
	err := viper.ReadInConfig()
	assert.Nil(t, err)

	// CustomConfigFilePath is not exists
	CustomConfigFilePath = ""
	InitConfig()

	// test env case
	envKey := Test
	envVal := Test
	os.Setenv("UNIFY-QUERY_TEST", envVal)
	assert.Equal(t, envVal, viper.Get(envKey))

	// test env case with `.`
	envKey = "test.key"
	envVal = Test
	os.Setenv("UNIFY-QUERY_TEST_KEY", envVal)
	assert.Equal(t, envVal, viper.Get(envKey))
}
