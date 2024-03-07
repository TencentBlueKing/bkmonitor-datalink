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

type TestMetaConfig struct{}

func (c *TestMetaConfig) Clean() error {
	return nil
}

func (c *TestMetaConfig) GetTaskConfigList() []define.TaskConfig {
	return nil
}

type ChildConfigSuite struct {
	suite.Suite
}

func (s *ChildConfigSuite) TestClean() {
	child := new(configs.ChildTaskMetaConfig)
	child.TaskMetaConfig = new(TestMetaConfig)
	child.Name = "zs"
	child.Version = "1.2.3"
	child.Type = "fff"
	err := child.Clean()
	s.Equal(nil, err)
}

func TestRun(t *testing.T) {
	suite.Run(t, &ChildConfigSuite{})
}
