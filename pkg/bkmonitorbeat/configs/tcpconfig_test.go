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
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestTCPTaskConfig(t *testing.T) {
	conf := configs.NewTCPTaskConfig()
	var taskConf define.TaskConfig = conf

	err := taskConf.Clean()
	if err != nil {
		t.Errorf(err.Error())
	}

	if taskConf.GetIdent() != conf.Ident {
		t.Errorf("ident error")
	}

	conf.TaskID = int32(rand.Int31())
	err = taskConf.Clean()
	if err != nil {
		t.Errorf("config clean failed: %v", err)
	}
}

func TestTCPMetaTaskConfig(t *testing.T) {
	conf := configs.NewTCPTaskMetaConfig(configs.NewConfig())
	conf.MaxTimeout = time.Second
	conf.MinPeriod = time.Minute
	conf.DataID = int32(rand.Int31())

	taskConf1 := configs.NewTCPTaskConfig()
	taskConf1.Timeout = 2 * time.Second
	taskConf1.Period = 2 * time.Minute
	ident1 := taskConf1.GetIdent()
	conf.Tasks = append(conf.Tasks, taskConf1)

	taskConf2 := configs.NewTCPTaskConfig()
	taskConf2.Timeout = time.Second
	taskConf2.Period = time.Second
	ident2 := taskConf2.GetIdent()
	conf.Tasks = append(conf.Tasks, taskConf2)

	var taskMetaConf define.TaskMetaConfig = conf

	err := taskMetaConf.Clean()
	if err != nil {
		t.Errorf("clean error: %v", err)
	}
	if ident1 == taskConf1.GetIdent() || taskConf1.Timeout > conf.MaxTimeout || taskConf1.Period < conf.MinPeriod || taskConf1.DataID != conf.DataID {
		t.Errorf("task1 clean error")
	}
	if ident2 == taskConf1.GetIdent() || taskConf2.Timeout > conf.MaxTimeout || taskConf2.Period < conf.MinPeriod || taskConf1.DataID != conf.DataID {
		t.Errorf("task2 clean error")
	}

	var configList []define.TaskConfig = conf.GetTaskConfigList()
	if len(configList) != 2 {
		t.Errorf("get tasks error")
	}

	for i, c := range configList {
		if c.GetTimeout() > conf.MaxTimeout || c.GetPeriod() < conf.MinPeriod || c.GetDataID() != conf.DataID {
			t.Errorf("tasks[%v] clean error", i)
		}
	}
}

// TCPConfiSuite :
type TCPConfiSuite struct {
	suite.Suite
}

// TestTCPConfig :
func TestTCPConfig(t *testing.T) {
	suite.Run(t, &TCPConfiSuite{})
}

// TestConfig :
func (s *TCPConfiSuite) TestConfigClean() {
	metaConf := configs.NewTCPTaskMetaConfig(configs.NewConfig())
	taskConf := new(configs.TCPTaskConfig)
	metaConf.Tasks = append(metaConf.Tasks, taskConf)

	s.NoError(metaConf.Clean(), "clean error")

	s.Equal(configs.DefaultBufferSize, metaConf.MaxBufferSize)
	s.Equal(define.DefaultTimeout, metaConf.MaxTimeout)
	s.Equal(define.DefaultPeriod, metaConf.MinPeriod)

	s.Equal(define.DefaultPeriod, taskConf.Period)
	s.Equal(metaConf.MaxTimeout, taskConf.Timeout)
	s.Equal(taskConf.Timeout, taskConf.AvailableDuration)
	s.Equal(configs.DefaultBufferSize, taskConf.BufferSize)
	s.Equal(metaConf.MinPeriod, taskConf.Period)
	s.Equal(metaConf.DataID, taskConf.DataID)
	s.Equal("", taskConf.Request)
	s.Equal("raw", taskConf.RequestFormat)
	s.Equal("", taskConf.Response)
	s.Equal("startswith", taskConf.ResponseFormat)
}
