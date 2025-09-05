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
	"reflect"
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Note: 所有的 Processor 需要在 controller/register 里进行 import

// Processor 代表着数据处理器
type Processor interface {
	// Name 返回采集器名称
	Name() string

	// IsDerived 标识处理器是否属于可派生类型
	IsDerived() bool

	// IsPreCheck 标识处理器是否处于预处理类型
	// 默认处理器中预处理类型的有 proxyvaliator/tokenchecker/ratelimiter/licensechecker
	IsPreCheck() bool

	// Process 方法会就地修改传入的 *define.Record，当且仅当需要衍生出另外的 Record 才会返回 *define.Record 实例
	Process(originalRecord *define.Record) (derivedRecord *define.Record, err error)

	// Reload 重载 processor 配置
	// 对于无状态的 processor 可支持替换实例所有变量
	// 对于有状态的 processor 需要`谨慎地`处理所有变量 避免内存/goroutines 泄漏
	Reload(config map[string]any, customized []SubConfigProcessor)

	// MainConfig 获取主配置信息
	MainConfig() map[string]any

	// SubConfigs 获取子配置信息
	SubConfigs() []SubConfigProcessor

	// Clean 清理 Processor
	Clean()
}

func MustLoadConfigs(content string) Configs {
	config, err := confengine.LoadConfigContent(content)
	if err != nil {
		panic(err)
	}

	var psc Configs
	err = config.UnpackChild("processor", &psc)
	if err != nil {
		panic(err)
	}
	if len(psc) == 0 {
		panic("no processor configs found")
	}

	return psc
}

func MustCreateFactory(content string, createFunc CreateFunc) Processor {
	psc := MustLoadConfigs(content)
	factory, err := createFunc(psc[0].Config, nil)
	if err != nil {
		panic(err)
	}
	return factory
}

func DiffMainConfig(src, dst map[string]any) bool {
	return reflect.DeepEqual(src, dst)
}

type DiffCustomizedResult struct {
	Keep    []SubConfigProcessor
	Updated []SubConfigProcessor
	Deleted []SubConfigProcessor
}

func DiffCustomizedConfig(src, dst []SubConfigProcessor) DiffCustomizedResult {
	type T struct {
		Token string
		Type  string
		ID    string
	}

	toMap := func(input []SubConfigProcessor) map[T]SubConfigProcessor {
		ret := make(map[T]SubConfigProcessor)
		for _, item := range input {
			ret[T{Token: item.Token, Type: item.Type, ID: item.ID}] = item
		}
		return ret
	}

	srcMap := toMap(src)
	dstMap := toMap(dst)

	var keep, updated, deleted []SubConfigProcessor
	for k, dstP := range dstMap {
		srcP, found := srcMap[k]
		// 原先不存在的 processor 标记为 'new'
		if !found {
			logger.Infof("diff: new processor '%s', token=%v, type=%v, id=%v", dstP.Config.Name, dstP.Token, dstP.Type, dstP.ID)
			updated = append(updated, dstP)
			continue
		}

		equal := reflect.DeepEqual(srcP.Config.Config, dstP.Config.Config)
		if !equal {
			// 原先存在 且内容有变更 标记为 'update'
			logger.Infof("diff: update processor '%s', token=%v, type=%v, id=%v", dstP.Config.Name, dstP.Token, dstP.Type, dstP.ID)
			updated = append(updated, dstP)
		} else {
			// 原先存在 但无内容变更 标记为 'keep'
			keep = append(keep, srcP)
		}
	}

	for k, srcP := range srcMap {
		_, found := dstMap[k]
		// 已经删除的 processor 标记为 'delete'
		if !found {
			logger.Infof("diff: delete processor '%s', token=%v, type=%v, id=%v", srcP.Config.Name, srcP.Token, srcP.Type, srcP.ID)
			deleted = append(deleted, srcP)
		}
	}
	return DiffCustomizedResult{
		Keep:    keep,
		Updated: updated,
		Deleted: deleted,
	}
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

type CreateFunc func(config map[string]any, customized []SubConfigProcessor) (Processor, error)

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
	mainConfig map[string]any
	subConfigs []SubConfigProcessor
}

func NewCommonProcessor(mainConfig map[string]any, subConfigs []SubConfigProcessor) CommonProcessor {
	return CommonProcessor{
		mainConfig: mainConfig,
		subConfigs: subConfigs,
	}
}

func (p CommonProcessor) MainConfig() map[string]any {
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
