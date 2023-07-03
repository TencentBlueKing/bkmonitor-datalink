// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cron

import (
	"context"
	"sync"

	"github.com/emirpasic/gods/maps/treemap"
	"github.com/roylee0704/gron"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Job :
type Job struct {
	ctx       context.Context
	scheduler *Scheduler
	task      define.Task
}

// Run :
func (j *Job) Run() {
	logger.Info("start gron job")
	j.task.Run(j.ctx, j.scheduler.EventChan)
}

type schedulerState struct {
	scheduler.BaseScheduler
	tasks    *treemap.Map
	taskLock sync.RWMutex
	wg       sync.WaitGroup

	ctx        context.Context
	cancelFunc context.CancelFunc

	gron *gron.Cron
}

// Wait :
func (s *schedulerState) Wait() {
	s.wg.Wait()
	for _, e := range s.gron.Entries() {
		job := e.Job.(*Job)
		job.task.Wait()
	}
	s.Status = define.SchedulerFinished
}

// Stop :
func (s *schedulerState) Stop() {
	s.gron.Stop()
	s.cancelFunc()
	s.Status = define.SchedulerTerminting
}

// Add :
func (s *schedulerState) Add(task define.Task) {
	s.taskLock.Lock()
	defer s.taskLock.Unlock()
	conf := task.GetConfig()
	s.tasks.Put(conf.GetIdent(), task)
}

// Count :
func (s *schedulerState) Count() int {
	return len(s.gron.Entries())
}

// Scheduler :
type Scheduler struct {
	*schedulerState
}

// Start :
func (s *Scheduler) Start(ctx context.Context) error {
	logger.Info("Scheduler.Start")
	state := s.schedulerState

	state.ctx, state.cancelFunc = context.WithCancel(ctx)
	state.wg.Add(1)
	state.Status = define.SchedulerRunning

	state.taskLock.RLock()
	g := state.gron
	tasks := state.tasks
	iter := tasks.Iterator()
	for iter.Next() {
		task := iter.Value().(define.Task)
		conf := task.GetConfig()
		job := &Job{
			ctx:       state.ctx,
			scheduler: s,
			task:      task,
		}
		go job.Run()
		// 然后开始周期调度
		g.Add(gron.Every(conf.GetPeriod()), job)
	}
	state.taskLock.RUnlock()

	go func() {
		<-state.ctx.Done()
		logger.Debug("scheduler context done")
		g.Stop()

		state.taskLock.RLock()
		iter := tasks.Iterator()
		logger.Debug("wait for tasks")
		for iter.Next() {
			task := iter.Value().(define.Task)
			task.Wait()
		}
		logger.Debug("wait tasks finished")
		state.taskLock.RUnlock()
		s.Status = define.SchedulerFinished
		state.wg.Done()
		logger.Debug("scheduler done")
	}()

	logger.Info("start gron")
	g.Start()

	return nil
}

// Reload :
func (s *Scheduler) Reload(ctx context.Context, conf define.Config, tasks []define.Task) error {
	logger.Info("Scheduler.Reload")
	oldState := s.schedulerState
	oldState.Stop()

	state := newState()
	state.Config = conf
	state.EventChan = oldState.EventChan
	state.Status = oldState.Status

	s.schedulerState = state

	for _, task := range tasks {
		s.Add(task)
	}

	oldState.Wait()
	err := s.Start(ctx)
	if err != nil {
		return err
	}

	logger.Info("Scheduler is running")

	return nil
}

func newState() *schedulerState {
	state := &schedulerState{
		tasks: treemap.NewWithStringComparator(),
		gron:  gron.New(),
	}
	state.Status = define.SchedulerReady
	return state
}

// New :
func New(bt define.Beater, config define.Config) define.Scheduler {
	s := &Scheduler{
		schedulerState: newState(),
	}
	s.EventChan = bt.GetEventChan()
	s.Config = config
	return s
}
