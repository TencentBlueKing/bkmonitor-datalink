// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package discover

import (
	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/notifier"
)

var bus = notifier.NewDefaultRateBus()

// Publish 发布 discover 变更信号
func Publish() {
	bus.Publish()
}

// Notify 接收 discover 变更信号
func Notify() <-chan struct{} {
	return bus.Subscribe()
}

// Discover 是监控资源监视器
// discover 负责启动对各类监控资源的 watch 操作并处理来自 prometheus discovery 的 targetgroups
type Discover interface {
	// Name 实例名称
	Name() string

	// UK 唯一标识 格式为 $Kind:..
	UK() string

	// Type 实例类型
	Type() string

	// IsSystem 是否为系统内置资源
	IsSystem() bool

	// Start 启动实例
	Start() error

	// Stop 停止实例
	Stop()

	// Reload 重载实例
	Reload() error

	// MonitorMeta 返回元数据信息
	MonitorMeta() define.MonitorMeta

	// DataID 获取 DataID 信息
	DataID() *bkv1beta1.DataID

	// SetDataID 更新 DataID 信息
	SetDataID(dataID *bkv1beta1.DataID)

	// DaemonSetChildConfigs 获取 daemonset 类型子配置信息
	DaemonSetChildConfigs() []*ChildConfig

	// StatefulSetChildConfigs 获取 statafulset 类型子配置信息
	StatefulSetChildConfigs() []*ChildConfig
}
