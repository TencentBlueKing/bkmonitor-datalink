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
	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/host"
)

// BeaterStatus
const (
	BeaterStatusUnknown     = -2
	BeaterStatusError       = -1
	BeaterStatusReady       = 0
	BeaterStatusRunning     = 1
	BeaterStatusTerminating = 2
	BeaterStatusTerminated  = 3

	// GatherStatus
	GatherStatusUnknown = -1
	GatherStatusOK      = 0
	GatherStatusError   = 1
)

// Beater : Beater
type Beater interface {
	Run() error
	Stop()
	Reload(*common.Config)
	GetEventChan() chan Event
	GetConfig() Config
	GetScheduler() Scheduler
}

var GlobalWatcher host.Watcher

type LogConfig struct {
	Stdout  bool   `config:"stdout"`
	Level   string `config:"level"`
	Path    string `config:"path"`
	MaxSize int    `config:"maxsize"`
	MaxAge  int    `config:"maxage"`
	Backups int    `config:"backups"`
}
