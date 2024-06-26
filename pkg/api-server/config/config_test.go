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
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// TestConfigParamsFromEnv Test config file
func TestConfigParamsFromEnv(t *testing.T) {
	FilePath = "../dev_config.yaml"
	InitConfig()

	expectedValue := "3"
	serviceNamePath := "http.port"
	envName := fmt.Sprintf("%s_%s", strings.ToUpper(EnvKeyPrefix), strings.ToUpper(strings.Replace(serviceNamePath, ".", "_", -1)))
	os.Setenv(envName, expectedValue)

	assert.Equal(t, expectedValue, strconv.Itoa(viper.GetInt(serviceNamePath)))
}
