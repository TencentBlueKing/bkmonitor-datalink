// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

// Note: 所有的 Processor 需要在 controller/register 里进行 import

// Processor 代表着数据处理器
type Processor interface {
	// Name 返回采集器名称
	Name() string

	// IsDerived 标识处理器是否属于可派生类型
	IsDerived() bool

	// IsPreCheck 标识处理器是否处于预处理类型，默认处理器中 tokenchecker/ratelimiter 为预处理类型
	IsPreCheck() bool

	// Process 方法会就地修改传入的 *define.Record，当且仅当需要衍生出另外的 Record 才会返回 *define.Record 实例
	Process(originalRecord *define.Record) (derivedRecord *define.Record, err error)

	// MainConfig 获取主配置信息
	MainConfig() map[string]interface{}

	// SubConfigs 获取子配置信息
	SubConfigs() []SubConfigProcessor

	// Clean 清理 Processor
	Clean()
}

// Instance 表示处理器实例
type Instance interface {
	// ID 返回实例唯一标识
	ID() string

	// Processor 实现处理器接口
	Processor
}

type instance struct {
	id string
	Processor
}

func (i instance) ID() string {
	return i.id
}

func NewInstance(id string, processor Processor) Instance {
	return instance{id: id, Processor: processor}
}

var processorsMap = map[string]CreateFunc{}

func register(name string, createFunc CreateFunc) error {
	_, ok := processorsMap[name]
	if ok {
		return errors.Errorf("duplicated processor: [%s]", name)
	}

	processorsMap[name] = createFunc
	return nil
}

type CreateFunc func(config map[string]interface{}, customized []SubConfigProcessor) (Processor, error)

// Register 注册 Processor
func Register(name string, createFunc CreateFunc) {
	if err := register(name, createFunc); err != nil {
		panic(err)
	}
}

// GetProcessorCreator 获取已经注册的 Processor
func GetProcessorCreator(name string) CreateFunc {
	n := strings.Split(name, "/")
	return processorsMap[n[0]]
}

type CommonProcessor struct {
	mainConfig map[string]interface{}
	subConfigs []SubConfigProcessor
}

func NewCommonProcessor(mainConfig map[string]interface{}, subConfigs []SubConfigProcessor) CommonProcessor {
	return CommonProcessor{
		mainConfig: mainConfig,
		subConfigs: subConfigs,
	}
}

func (p CommonProcessor) MainConfig() map[string]interface{} {
	return p.mainConfig
}

func (p CommonProcessor) SubConfigs() []SubConfigProcessor {
	return p.subConfigs
}

func (p CommonProcessor) Clean() {}

var nonSchedRecords = define.NewRecordQueue(define.PushModeGuarantee)

func PublishNonSchedRecords(r *define.Record) {
	nonSchedRecords.Push(r)
}

func NonSchedRecords() <-chan *define.Record {
	return nonSchedRecords.Get()
}
