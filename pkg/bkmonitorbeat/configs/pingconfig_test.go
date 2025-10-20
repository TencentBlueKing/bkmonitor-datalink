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

// PingConfiSuite :
type PingConfiSuite struct {
	suite.Suite
}

// TestPingConfig :
func TestPingConfig(t *testing.T) {
	suite.Run(t, &PingConfiSuite{})
}

// TestConfig :
func (s *PingConfiSuite) TestConfigClean() {
	metaConf := configs.NewPingTaskMetaConfig(configs.NewConfig())
	taskConf := configs.NewPingTaskConfig()
	taskConf.Targets = []*configs.Target{
		{
			"127.0.0.1",
			"ip",
			nil,
		},
	}
	metaConf.Tasks = append(metaConf.Tasks, taskConf)

	s.NoError(metaConf.Clean(), "clean error")

	s.Equal(define.DefaultTimeout, metaConf.MaxTimeout)
	s.Equal(define.DefaultPeriod, metaConf.MinPeriod)

	s.Equal(define.DefaultPeriod, taskConf.Period)
	s.Equal(define.DefaultTimeout, taskConf.Timeout)
	s.Equal(false, taskConf.CustomReport)
}
