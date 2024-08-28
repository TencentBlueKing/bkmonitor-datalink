// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package configs 所有任务Config的基类文件
package configs

import (
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// BaseTaskParam 任务基础信息,实现了TaskConfig中除Clean以外的所有接口，实现了CompositableConfig的CleanConfig接口，以便进行配置批量清洗操作
type BaseTaskParam struct {
	Ident             string              `config:"ident,ignore"`
	DataID            int32               `config:"dataid"`
	BizID             int32               `config:"bk_biz_id"`
	TaskID            int32               `config:"task_id" validate:"required"`
	Timeout           time.Duration       `config:"timeout"`
	AvailableDuration time.Duration       `config:"available_duration"`
	Period            time.Duration       `config:"period" validate:"min=1s"`
	Labels            []map[string]string `config:"labels"`
	Tags              map[string]string   `config:"tags"`

	labels []map[string]string
	Sorted define.Tags
}

func (t *BaseTaskParam) convertLabels() {
	// 清空Ident,屏蔽之前计算Ident带来的误差
	t.SetIdent("")

	// 将Labels转换为Tags，用于Hash计算
	list := make(define.Tags, 0)
	for _, labelItem := range t.Labels {
		for k, v := range labelItem {
			tag := define.Tag{
				Key:   k,
				Value: v,
			}
			list = append(list, tag)
		}
	}

	sort.Sort(list)
	t.Sorted = list

	// 将label置于隐藏字段
	t.labels = t.Labels
	// 将Labels置空
	t.Labels = make([]map[string]string, 0)
}

func (t *BaseTaskParam) resetLabels() {
	// 充填Labels
	t.Labels = t.labels

	// 将Tags置空
	t.Sorted = make(define.Tags, 0)
}

// SetIdent :
func (t *BaseTaskParam) SetIdent(ident string) {
	t.Ident = ident
}

// initIdent :
func (t *BaseTaskParam) initIdent(conf define.TaskConfig) error {
	// 将map形态的labels转换为排序好的数组,屏蔽map遍历时顺序不确定的问题
	t.convertLabels()
	// 计算Hash
	t.Ident = utils.HashIt(conf)

	// 恢复labels
	t.resetLabels()
	return nil
}

// CleanParams  CompositableParam接口,用于批量清洗
func (t *BaseTaskParam) CleanParams() error {
	if t.Timeout < time.Nanosecond {
		logger.Infof("timeout will be set default,timeout %v", t.Timeout)
		t.Timeout = define.DefaultTimeout
	}

	if t.AvailableDuration < time.Nanosecond {
		t.AvailableDuration = t.Timeout
	}

	if t.Period < time.Nanosecond {
		t.Period = define.DefaultPeriod
	}

	return nil
}

// GetDataID 获取用于上报数据的dataid
func (t *BaseTaskParam) GetDataID() int32 {
	return t.DataID
}

// GetLabels 获取注入的标签
func (t *BaseTaskParam) GetLabels() []map[string]string {
	return t.Labels
	// return make(map[string]string)
}

// GetIdent 获取任务实体标识
func (t *BaseTaskParam) GetIdent() string {
	return t.Ident
}

// GetTimeout 获取任务超时时间
func (t *BaseTaskParam) GetTimeout() time.Duration {
	return t.Timeout
}

// GetAvailableDuration 获取可用区间
func (t *BaseTaskParam) GetAvailableDuration() time.Duration {
	return t.AvailableDuration
}

// GetPeriod 获取任务执行周期
func (t *BaseTaskParam) GetPeriod() time.Duration {
	return t.Period
}

// GetBizID 获取业务ID
func (t *BaseTaskParam) GetBizID() int32 {
	return t.BizID
}

// GetTaskID 获取任务ID
func (t *BaseTaskParam) GetTaskID() int32 {
	return t.TaskID
}

// NewBaseTaskParam :
func NewBaseTaskParam() BaseTaskParam {
	return BaseTaskParam{}
}

// BaseTaskMetaParam : 基础组任务信息
type BaseTaskMetaParam struct {
	DataID     int32         `config:"dataid" validate:"required"`
	MaxTimeout time.Duration `config:"max_timeout" validate:"min=1s"`
	MinPeriod  time.Duration `config:"min_period" validate:"min=1s"`
}

// CleanParams :
func (c *BaseTaskMetaParam) CleanParams() error {
	if c.MaxTimeout < time.Second {
		c.MaxTimeout = define.DefaultTimeout
	}
	if c.MinPeriod < time.Second {
		c.MinPeriod = define.DefaultPeriod
	}
	return nil
}

// CleanTask :
func (c *BaseTaskMetaParam) CleanTask(task define.TaskConfig) error {
	err := task.Clean()
	if err != nil {
		return err
	}
	bt, ok := utils.GetPtrByName(task, "BaseTaskParam")
	if !ok {
		panic(define.ErrType)
	}
	baseTask := bt.(*BaseTaskParam)

	if c.MaxTimeout < baseTask.Timeout || baseTask.Timeout == 0 {
		logger.Infof("timeout %v less than max timeout %v, timeout will  be set max timeout", baseTask.Timeout, c.MaxTimeout)
		baseTask.Timeout = c.MaxTimeout
	}

	if c.MinPeriod > baseTask.Period {
		baseTask.Period = c.MinPeriod
	}
	if baseTask.Period < baseTask.Timeout {
		baseTask.Timeout = baseTask.Period
	}

	if baseTask.DataID == 0 {
		baseTask.DataID = c.DataID
	}
	err = task.InitIdent()
	if err != nil {
		return err
	}
	return nil
}

// GetDataID :
func (c *BaseTaskMetaParam) GetDataID() int32 {
	return c.DataID
}

// NewBaseTaskMetaParam :
func NewBaseTaskMetaParam() BaseTaskMetaParam {
	return BaseTaskMetaParam{
		DataID:     0,
		MaxTimeout: define.DefaultTimeout,
		MinPeriod:  define.DefaultPeriod,
	}
}
