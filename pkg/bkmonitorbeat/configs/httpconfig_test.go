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

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// HTTPConfiSuite :
type HTTPConfiSuite struct {
	suite.Suite
}

// TestHTTPConfig :
func TestHTTPConfig(t *testing.T) {
	suite.Run(t, &HTTPConfiSuite{})
}

// TestConfig :
func (s *HTTPConfiSuite) TestConfigClean() {
	metaConf := configs.NewHTTPTaskMetaConfig(configs.NewConfig())
	taskConf := configs.NewHTTPTaskConfig()
	stepConf := new(configs.HTTPTaskStepConfig)
	stepConf.URL = "bk.tencent.com"
	taskConf.Steps = append(taskConf.Steps, stepConf)
	metaConf.Tasks = append(metaConf.Tasks, taskConf)

	s.NoError(metaConf.Clean(), "clean error")

	s.Equal(configs.DefaultBufferSize, metaConf.MaxBufferSize)
	s.Equal(define.DefaultTimeout, metaConf.MaxTimeout)
	s.Equal(define.DefaultPeriod, metaConf.MinPeriod)

	s.Equal(define.DefaultPeriod, taskConf.Period)
	s.Equal(define.DefaultTimeout, taskConf.Timeout)
	s.Equal(configs.DefaultBufferSize, taskConf.BufferSize)
	s.Equal(taskConf.Timeout, taskConf.AvailableDuration)
	s.Equal("", taskConf.Proxy)
	s.Equal(false, taskConf.InsecureSkipVerify)
	s.Equal(false, taskConf.CustomReport)

	s.Equal("http://bk.tencent.com", stepConf.URL)
	s.Equal("GET", stepConf.Method)
	s.Equal("200", stepConf.ResponseCode)
	s.Len(stepConf.ResponseCodeList, 1)
	s.Equal(200, stepConf.ResponseCodeList[0])
	s.Equal("", stepConf.Request)
	s.Equal("raw", stepConf.RequestFormat)
	s.Equal("", stepConf.Response)
	s.Equal("startswith", stepConf.ResponseFormat)
}
