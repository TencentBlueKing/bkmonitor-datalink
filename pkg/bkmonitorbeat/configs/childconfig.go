// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ChildTaskConfig 额外封装一层子配置参数,用于计算ident，不实际应用到任务列表
type ChildTaskConfig struct {
	PreIdent string
	Version  string
	Name     string
	Type     string
}

// ChildTaskMetaConfig 子任务config，在基础任务模板上添加子任务记录信息
type ChildTaskMetaConfig struct {
	define.TaskMetaConfig `config:"_,inline"`
	Version               string `config:"version"`
	Name                  string `config:"name"`
	Type                  string `config:"type"`
	Path                  string
}

// Clean :
func (c *ChildTaskMetaConfig) Clean() error {
	if err := c.TaskMetaConfig.Clean(); err != nil {
		logger.Errorf("clean child task failed,error:%v", err.Error())
		return err
	}
	if c.Name == "" {
		logger.Errorf("clean child task failed,error:%v", define.ErrNoName.Error())
		return define.ErrNoName
	}
	if c.Version == "" {
		logger.Errorf("clean child task failed,error:%v", define.ErrNoVersion.Error())
		return define.ErrNoVersion
	}
	tasks := c.GetTaskConfigList()
	for _, v := range tasks {
		// 封装一层参数，再取一次ident标识
		conf := ChildTaskConfig{
			PreIdent: v.GetIdent(),
			Name:     c.Name,
			Type:     c.Type,
			Version:  c.Version,
		}
		ident := utils.HashIt(conf)
		v.SetIdent(ident)
	}

	return nil
}
