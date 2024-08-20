// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package checker

import (
	"context"
	"sync"
	"time"

	"github.com/emirpasic/gods/maps/treemap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// CheckScheduler :
type CheckScheduler struct {
	scheduler.BaseScheduler

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	tasks  *treemap.Map

	PreRun  utils.HookManager
	PostRun utils.HookManager
}

// Add : add task
func (s *CheckScheduler) Add(task define.Task) {
	conf := task.GetConfig()
	s.tasks.Put(conf.GetIdent(), task)
}

// Stop :
func (s *CheckScheduler) Stop() {
	logger.Info("CheckScheduler scheduler stop")
	s.Status = define.SchedulerTerminting
	s.cancel()
}

// Wait :
func (s *CheckScheduler) Wait() {
	logger.Info("CheckScheduler scheduler wait")
	s.wg.Wait()
	s.Status = define.SchedulerFinished
}

// IsDaemon :
func (s *CheckScheduler) IsDaemon() bool {
	return false
}

// Count :
func (s *CheckScheduler) Count() int {
	return s.tasks.Size()
}

// Start :
func (s *CheckScheduler) Start(ctx context.Context) error {
	s.wg.Add(1)
	ctx, cancel := context.WithCancel(ctx)
	s.ctx = ctx
	s.cancel = cancel
	go s.Run(s.ctx)
	return nil
}

// Run :
func (s *CheckScheduler) Run(ctx context.Context) {
	s.Status = define.SchedulerRunning
	s.PreRun.Apply(ctx)
	defer s.PostRun.Apply(ctx)

	if s.tasks.Size() == 0 {
		panic(define.ErrTaskNotFound)
	}

	logger.Infof("CheckScheduler checking %d tasks", s.tasks.Size())
	iter := s.tasks.Iterator()
	for iter.Next() {
		task := iter.Value().(define.Task)
		task.Run(ctx, s.EventChan)
	}
	// for test
	time.Sleep(2 * time.Second)
	s.Status = define.SchedulerFinished
	s.wg.Done()
}

// New :
func New(bt define.Beater, config define.Config) define.Scheduler {
	s := &CheckScheduler{
		tasks: treemap.NewWithStringComparator(),
	}
	s.Config = config
	s.EventChan = bt.GetEventChan()
	s.PostRun.Add(func(ctx context.Context) {
		bt.Stop()
	})
	return s
}
