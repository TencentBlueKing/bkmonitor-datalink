// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package listen

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Scheduler 专门为监听类型的任务提供的调度器
type Scheduler struct {
	scheduler.BaseScheduler

	tasks      map[string]define.Task
	taskCancel map[string]context.CancelFunc

	ctx       context.Context
	ctxCancel context.CancelFunc
	wg        sync.WaitGroup
}

const (
	restartInterval = 10 * time.Second
)

func (s *Scheduler) Add(t define.Task) {
	s.tasks[t.GetConfig().GetIdent()] = t
}

// 保持监听，退出了就再启动
func (s *Scheduler) keepListen(ctx context.Context, task define.Task) {
	// 生成针对单个任务的关闭触发器
	subCtx, cancel := context.WithCancel(ctx)
	s.taskCancel[task.GetConfig().GetIdent()] = cancel
	logger.Infof("keep listen task: %v", task.GetTaskID())
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-subCtx.Done():
				return
			default:
				time.Sleep(restartInterval)
				s.listen(subCtx, task)
			}
		}
	}()
}

// 启动监听，通过recover保证该函数退出不影响主进程
func (s *Scheduler) listen(ctx context.Context, task define.Task) {
	defer func() {
		if p := recover(); p != nil {
			logger.Errorf("task:%d panic with error:%v", task.GetTaskID(), p)
			// 打印堆栈
			var buf [8192]byte
			n := runtime.Stack(buf[:], false)
			logger.Errorf("panic stack ==> %s", buf[:n])
		}
	}()
	logger.Infof("task: %v start listen", task.GetTaskID())
	task.Run(ctx, s.EventChan)
}

func (s *Scheduler) Start(ctx context.Context) error {
	defer utils.RecoverFor(func(err error) {
		logger.Errorf("listen scheduler crash: %v", errors.WithStack(err))
	})

	logger.Info("start listen collect")
	s.ctx, s.ctxCancel = context.WithCancel(ctx)
	for _, task := range s.tasks {
		s.keepListen(s.ctx, task)
	}
	s.Status = define.SchedulerRunning
	return nil
}

func (s *Scheduler) IsDaemon() bool { return false }

func (s *Scheduler) Count() int { return len(s.tasks) }

func (s *Scheduler) GetStatus() define.Status { return s.Status }

func (s *Scheduler) Stop() {
	s.Status = define.SchedulerTerminting
	s.ctxCancel()
}

func (s *Scheduler) Wait() {
	s.Status = define.SchedulerFinished
	s.wg.Wait()
}

func (s *Scheduler) Reload(ctx context.Context, conf define.Config, newTasks []define.Task) error {
	logger.Info("listen scheduler reload")
	deleteList := make([]string, 0)
	for key := range s.tasks {
		exist := false
		for _, newTask := range newTasks {
			if newTask.GetConfig().GetIdent() == key {
				exist = true
				break
			}
		}
		// 新任务里不存在该任务，则需要关闭
		if !exist {
			deleteList = append(deleteList, key)
		}
	}

	addList := make([]define.Task, 0)
	for _, newTask := range newTasks {
		exist := false
		for key := range s.tasks {
			if newTask.GetConfig().GetIdent() == key {
				exist = true
				break
			}
		}
		// 新增该任务
		if !exist {
			addList = append(addList, newTask)
		}
	}

	// 由于涉及到端口占用，只能先删后增
	logger.Infof("listen scheduler remove %d tasks", len(deleteList))
	s.removeTasks(deleteList)
	logger.Infof("listen scheduler start %d tasks", len(addList))
	s.addTasks(addList)
	return nil
}

func (s *Scheduler) removeTasks(taskIndents []string) {
	for _, indent := range taskIndents {
		cancelFunc := s.taskCancel[indent]
		cancelFunc()
		logger.Infof("listen scheduler remove task: %s", indent)
		delete(s.tasks, indent)
	}
}

func (s *Scheduler) addTasks(tasks []define.Task) {
	for _, task := range tasks {
		s.Add(task)
		s.keepListen(s.ctx, task)
	}
}

// New :
func New(bt define.Beater, config define.Config) define.Scheduler {
	s := &Scheduler{}
	s.EventChan = bt.GetEventChan()
	s.Config = config
	s.tasks = make(map[string]define.Task)
	s.taskCancel = make(map[string]context.CancelFunc)
	return s
}
