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
	"strconv"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// TestConfigParamsFromEnv
func TestConfigParamsFromEnv(t *testing.T) {
	defaultConfigPath = "../crontask.yaml"
	InitConfig()

	expectedValue := "3"
	serviceNamePath := "service.worker_number"
	os.Setenv("CRON_TASK_SERVICE_WORKER_NUMBER", expectedValue)
	assert.Equal(t, expectedValue, strconv.Itoa(viper.GetInt(serviceNamePath)))

	os.Unsetenv("CRON_TASK_SERVICE_WORKER_NUMBER")
	os.Setenv("SERVICE_WORKER_NUMBER", expectedValue)
	assert.Equal(t, 2, viper.GetInt(serviceNamePath))
}

func TestConfigParamsFromFile(t *testing.T) {
	InitConfig()

	serviceNamePath := "service.worker_number"
	expectedValue := 2
	assert.Equal(t, expectedValue, viper.GetInt(serviceNamePath))
}
