// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// BaseScheduler :
type BaseScheduler struct {
	Status    define.Status
	EventChan chan<- define.Event
	Config    define.Config
}

// Stop :
func (s *BaseScheduler) Stop() { s.Status = define.SchedulerTerminting }

// Wait :
func (s *BaseScheduler) Wait() { s.Status = define.SchedulerFinished }

// GetStatus :
func (s *BaseScheduler) GetStatus() define.Status { return s.Status }

// IsDaemon :
func (s *BaseScheduler) IsDaemon() bool { return true }

// Start :
func (s *BaseScheduler) Start(ctx context.Context) error {
	s.Status = define.SchedulerRunning
	return nil
}

// Reload :
func (s *BaseScheduler) Reload(ctx context.Context, config define.Config, tasks []define.Task) error {
	s.Config = config
	return nil
}
