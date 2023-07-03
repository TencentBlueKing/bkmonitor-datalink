// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"time"

	"github.com/elastic/beats/libbeat/common"
)

const (
	DefaultTimeout                         = 3 * time.Second  // 默认任务超时
	DefaultPeriod                          = 10 * time.Second //默认任务执行间隔
	DefaultTaskConcurrencyLimitPerInstance = 100000           // 默认任务单实例并发限制
	DefaultTaskConcurrencyLimitPerTask     = 1000             // 默认单个任务并发限制
)

// CompositeConfig :
type CompositeConfig interface {
	CleanConfig() error
}

// CompositeParam :
type CompositeParam interface {
	CleanParams() error
}

// TaskConfig : task config
type TaskConfig interface {
	GetBizID() int32
	GetIdent() string
	InitIdent() error
	SetIdent(ident string)
	GetTimeout() time.Duration
	GetAvailableDuration() time.Duration
	GetDataID() int32
	GetTaskID() int32
	GetPeriod() time.Duration
	GetType() string
	GetLabels() []map[string]string
	Clean() error
}

// TaskMetaConfig : task config
type TaskMetaConfig interface {
	GetTaskConfigList() []TaskConfig
	Clean() error
}

// Config : task config
type Config interface {
	GetTaskConfigListByType(string) []TaskConfig
	Clean() error
}

// ConfigEngine 配置引擎的接口
type ConfigEngine interface {
	Init(cfg *common.Config, bt Beater) error
	ReInit(cfg *common.Config) error
	GetTaskConfigList() []TaskConfig
	GetTaskNum() int
	GetWrongTaskNum() int
	CleanTaskConfigList() error
	RefreshHeartBeat() error
	SendHeartBeat() error
	GetGlobalConfig() Config
	HasChildPath() bool
}
