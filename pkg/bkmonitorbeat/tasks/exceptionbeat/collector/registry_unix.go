// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos
// +build aix darwin dragonfly freebsd linux netbsd openbsd solaris zos

package collector

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

const (
	DiskROEventType    = 3
	DiskSpaceEventType = 6
	CoreEventType      = 7
	OutOfMemEventType  = 9
)

// Collector interface define the basic function interface that will be used by beater.go.
// A collector is used to control the logic of event collection, for example, disk readonly event
// collection
type Collector interface {
	// Start Start the exception event collector
	Start(ctx context.Context, e chan<- define.Event, beatConfig *configs.ExceptionBeatConfig)
	// Reload reload the config of this collector, for example, stop or change the time interval
	Reload(*configs.ExceptionBeatConfig)
	// Stop stops the life-cycle of the colletor
	Stop()
}

var collectors []Collector

func GetMethods() []Collector {
	return collectors
}

func RegisterCollector(plugin Collector) {
	collectors = append(collectors, plugin)
}
