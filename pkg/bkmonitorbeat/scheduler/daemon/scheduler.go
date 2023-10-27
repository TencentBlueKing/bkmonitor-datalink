// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/emirpasic/gods/maps/treemap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type daemonState struct {
	scheduler.BaseScheduler

	taskLock  sync.RWMutex
	tasks     *treemap.Map
	jobs      JobQueue
	jobAtomic utils.Atomic
	ticker    *time.Ticker

	ctx        context.Context
	cancelFunc context.CancelFunc
}

// Stop :
func (s *daemonState) Stop() {
	s.Status = define.SchedulerTerminting
}

func newDaemonState() *daemonState {
	state := &daemonState{
		tasks: treemap.NewWithStringComparator(),
		jobs:  NewLockQueue(),
	}
	return state
}

// Daemon :
type Daemon struct {
	*daemonState
	waitgroup sync.WaitGroup
}

// Add :
func (s *Daemon) Add(task define.Task) {
	conf := task.GetConfig()
	s.taskLock.Lock()
	s.tasks.Put(conf.GetIdent(), task)
	s.taskLock.Unlock()
	if s.GetStatus() == define.SchedulerRunning {
		s.addJob(s.makeJobfromTask(task))
	}
}

func (s *Daemon) addJob(job Job) {
	s.jobs.Push(job)
}

// Wait :
func (s *Daemon) Wait() {
	s.waitgroup.Wait()
	for _, job := range s.jobs.PopAll() {
		task := job.GetTask()
		task.Wait()
	}
	s.Status = define.SchedulerFinished
}

func (s *Daemon) makeJobfromTask(task define.Task) Job {
	job := NewJob(task, s)
	job.Init()
	return job
}

func (s *Daemon) reloadJobFromTask(job Job, task define.Task) Job {
	jobTask := job.GetTask()
	jobTask.SetGlobalConfig(task.GetGlobalConfig())
	jobTask.SetConfig(task.GetConfig())
	// scheduler在job创建时已经绑定，这里不考虑scheduler改变的情况
	// 如果取消掉这句的注释，会造成reload时事件类采集任务ctx丢失，导致reload时无法正确关闭旧任务,注意
	// job.SetScheduler(s)
	job.Reload()
	return job
}

// Start :
func (s *Daemon) Start(ctx context.Context) error {
	state := s.daemonState

	state.ctx, state.cancelFunc = context.WithCancel(ctx)
	state.Status = define.SchedulerRunning

	state.taskLock.RLock()
	tasks := state.tasks
	jobs := make([]Job, 0)
	iter := tasks.Iterator()
	for iter.Next() {
		task := iter.Value().(define.Task)
		job := s.makeJobfromTask(task)
		jobs = append(jobs, job)
	}
	state.taskLock.RUnlock()

	s.jobs.Push(jobs...)

	globalConfig := s.Config.(*configs.Config)
	state.ticker = time.NewTicker(globalConfig.CheckInterval)

	s.waitgroup.Add(1)
	go s.run(ctx)
	return nil
}

// Count :
func (s *Daemon) Count() int {
	state := s.daemonState
	state.taskLock.RLock()
	defer state.taskLock.RUnlock()
	return state.tasks.Size()
}

func (s *Daemon) run(ctx context.Context) {
	defer utils.RecoverFor(func(err error) {
		logger.Errorf("scheduler crash: %v", err)
	})
	s.Status = define.SchedulerRunning
	logger.Debug("Daemon scheduler is running...")

loop:
	for s.Status == define.SchedulerRunning {
		ticker := s.ticker
		logger.Debug("waiting jobs")
		select {
		case <-ctx.Done():
			logger.Debug("context done")
			ticker.Stop()
			break loop
		case now, ok := <-ticker.C:
			if !ok {
				logger.Debug("ticker not ready, skip...")
				break // ticker stoped by reload
			}
			state := s.daemonState
			jobsQ := state.jobs
			state.jobAtomic.Run(func() {
				// 判断有哪些任务是需要跳出执行的
				jobs := jobsQ.PopUntil(now)
				jobLen := len(jobs)
				if jobLen <= 0 {
					return
				}
				logger.Infof("scheduler ready to run %v jobs", jobLen)
				for _, job := range jobs {
					go func(job Job) {
						task := job.GetTask()
						taskID := task.GetTaskID()
						defer utils.RecoverFor(func(err error) {
							logger.Errorf("run task %v panic: %v", taskID, err)
						})
						logger.Debugf("scheduler running job: %v", taskID)
						job.Run(state.EventChan)
					}(job)
				}
				for _, job := range jobs {
					// 计算任务的下一次执行时间
					job.Next()
				}
				jobsQ.Push(jobs...)
			})
		}
	}

	logger.Infof("scheduler stop with status: %v", s.Status)
	s.waitgroup.Done()
}

// Reload :
func (s *Daemon) Reload(ctx context.Context, conf define.Config, tasks []define.Task) error {
	logger.Debug("Daemon.Reload")

	oldState := s.daemonState

	state := newDaemonState()
	state.ticker = oldState.ticker
	state.Status = oldState.Status
	state.Config = conf
	state.EventChan = oldState.EventChan
	state.ctx = oldState.ctx
	state.cancelFunc = oldState.cancelFunc

	logger.Debugf("loaded %v tasks", len(tasks))
	taskMaps := treemap.NewWithStringComparator()
	// 写入新任务到新的任务列表,并生成一个map做ident去重
	for _, task := range tasks {
		conf := task.GetConfig()
		ident := conf.GetIdent()
		taskMaps.Put(ident, task)
		state.tasks.Put(ident, task)
	}

	// 旧列表遍历，与新列表重合的任务会被加入到job中
	oldState.jobAtomic.Run(func() {
		jobs := oldState.jobs.PopAll()
		for i := 0; i < len(jobs); i++ {
			job := jobs[i]
			jobTask := job.GetTask()
			conf := jobTask.GetConfig()
			ident := conf.GetIdent()
			task, ok := taskMaps.Get(ident)
			if ok {
				state.jobs.Push(s.reloadJobFromTask(job, task.(define.Task)))
				// 因为加入了旧任务，所以把同id的新任务删除
				taskMaps.Remove(ident)
			} else {
				job.Stop()
			}
		}
	})

	// 遍历剩余的新任务列表，加入到job中
	iter := taskMaps.Iterator()
	for iter.Next() {
		task := iter.Value().(define.Task)
		state.jobs.Push(s.makeJobfromTask(task))
	}

	logger.Infof("pushed %v new task into job queue", taskMaps.Size())

	s.daemonState = state
	logger.Debug("replaced state")

	return nil
}

// New :
func New(bt define.Beater, config define.Config) define.Scheduler {
	state := newDaemonState()
	state.EventChan = bt.GetEventChan()
	state.Config = config

	daemon := &Daemon{
		daemonState: state,
	}
	return daemon
}
