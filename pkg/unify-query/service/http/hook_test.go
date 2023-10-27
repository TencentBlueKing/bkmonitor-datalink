// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestRunAndReload
func TestRunAndReload(t *testing.T) {
	log.InitTestLogger()
	setDefaultConfig()
	gin.SetMode(gin.DebugMode)

	ctx := context.Background()

	service := Service{}
	LoadConfig()
	service.Reload(ctx)

	baseURL := fmt.Sprintf("http://%s:%d", IPAddress, Port)

	getPath := []string{PrometheusPathConfigPath}
	time.Sleep(3 * time.Second)
	for _, path := range getPath {
		response, err := http.DefaultClient.Get(baseURL + viper.GetString(path))
		assert.Nil(t, err)
		assert.Equal(t, 200, response.StatusCode)
	}

	response, err := http.DefaultClient.Get(baseURL + viper.GetString(ProfilePathConfigPath))
	assert.Nil(t, err)
	assert.Equal(t, 404, response.StatusCode)

	viper.Set(EnablePrometheusConfigPath, false)
	service.Reload(ctx)

	time.Sleep(3 * time.Second)
	response, err = http.DefaultClient.Get(baseURL + viper.GetString(PrometheusPathConfigPath))
	assert.Nil(t, err)
	assert.Equal(t, 404, response.StatusCode)
}
