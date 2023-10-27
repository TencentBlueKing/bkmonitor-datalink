// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package keyword

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/input"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/input/file"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/sender"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Scheduler struct {
	scheduler.BaseScheduler

	tasks map[string]*keyword.TaskConfig

	ctx       context.Context
	ctxCancel context.CancelFunc
}

func (s *Scheduler) Add(t define.Task) {
	taskConfig := s.taskToKeywordTask(t)
	s.tasks[taskConfig.TaskID] = taskConfig
}

func (s *Scheduler) Start(ctx context.Context) error {
	defer utils.RecoverFor(func(err error) {
		logger.Errorf("KeywordScheduler scheduler crash: %v", errors.WithStack(err))
	})

	logger.Info("start keyword collect")

	var (
		err    error
		states []file.State
	)

	s.ctx, s.ctxCancel = context.WithCancel(ctx)

	if s.Count() == 0 {
		logger.Warn("no keyword task running")
	}

	// only one input
	input.SingleInstance, err = input.New(s.ctx, s.tasks, states)
	if err != nil {
		logger.Errorf("new input failed, %v", err)
		return err
	}

	s.addTasks(s.tasks)

	err = input.SingleInstance.Start()
	if err != nil {
		logger.Errorf("input module start failed, %v", err)
		return err
	}

	s.Status = define.SchedulerRunning
	return nil
}

func (s *Scheduler) IsDaemon() bool {
	return false
}

func (s *Scheduler) Count() int {
	return len(s.tasks)
}

func (s *Scheduler) Stop() {
	s.Status = define.SchedulerTerminting
}

func (s *Scheduler) Wait() {
	s.Status = define.SchedulerFinished
}

func (s *Scheduler) GetStatus() define.Status {
	return s.Status
}

func (s *Scheduler) Reload(ctx context.Context, conf define.Config, newTasks []define.Task) error {
	logger.Infof("[Reload]update config, current tasks=>%d", len(s.tasks))

	removeTasks := make(map[string]*keyword.TaskConfig)
	addTasks := make(map[string]*keyword.TaskConfig)

	//step 1: 生成原来的任务清单
	for taskID, task := range s.tasks {
		removeTasks[taskID] = task
	}

	//step 2: 根据新配置找出有变动的任务列表
	for _, newTask := range newTasks {
		taskConfig := s.taskToKeywordTask(newTask)
		taskId := taskConfig.TaskID
		if originTaskConfig, ok := s.tasks[taskConfig.TaskID]; ok {
			if originTaskConfig.Same(taskConfig) {
				logger.Debugf("ignore secondary config file: %s, for already exists", taskId)
				delete(removeTasks, taskId)
				continue
			} else {
				logger.Infof("load modified secondary config file: %s", taskId)
				addTasks[taskId] = taskConfig
			}
		} else {
			logger.Infof("load new secondary config file: %s", taskId)
			addTasks[taskId] = taskConfig
		}
	}

	logger.Infof("[Reload]removeTasks=>%d, addTasks=>%d", len(removeTasks), len(addTasks))

	//step 3：清理任务信息
	if len(removeTasks) > 0 {
		s.removeTasks(removeTasks)
	}

	//step 4：对新增的任务进行采集
	if len(addTasks) > 0 {
		s.addTasks(addTasks)
	}

	//step 5: reload input module
	if input.SingleInstance != nil {
		input.SingleInstance.Reload(addTasks)
	}
	return nil
}

// New :
func New(bt define.Beater, config define.Config) define.Scheduler {
	s := &Scheduler{}
	s.EventChan = bt.GetEventChan()
	s.Config = config
	s.tasks = make(map[string]*keyword.TaskConfig)
	return s
}

func (s *Scheduler) removeTasks(tasks map[string]*keyword.TaskConfig) {
	for taskID, taskConfig := range tasks {
		if taskConfig.CtxCancel == nil {
			logger.Errorf("[removeTasks]task context is not init")
		} else {
			taskConfig.CtxCancel()
		}
		delete(s.tasks, taskID)
	}
}

func (s *Scheduler) addTasks(tasks map[string]*keyword.TaskConfig) {
	for taskID, task := range tasks {
		task.IPLinker = make(chan interface{})
		task.PSLinker = make(chan interface{})
		task.Ctx, task.CtxCancel = context.WithCancel(s.ctx)
		task.Ctx = context.WithValue(task.Ctx, "taskID", taskID)

		if _, ok := s.tasks[taskID]; !ok {
			s.tasks[taskID] = task
		}

		sm, err := sender.New(task.Ctx, task.Sender, s.EventChan)
		if err != nil {
			logger.Errorf("new sender failed, %v", err)
			continue
		}
		sm.AddInput(task.PSLinker)

		pm, err := processor.New(task.Ctx, task.Processer, task.TaskType)
		if err != nil {
			logger.Errorf("new process failed, %v", err)
			continue
		}
		pm.AddInput(task.IPLinker)
		pm.AddOutput(task.PSLinker)

		err = sm.Start()
		if err != nil {
			logger.Errorf("start sender failed, %v", err)
		}
		err = pm.Start()
		if err != nil {
			logger.Errorf("start process failed, %v", err)
		}
	}
}

func (s *Scheduler) taskToKeywordTask(t define.Task) *keyword.TaskConfig {
	c := t.GetConfig().(*configs.KeywordTaskConfig)
	taskConfig := keyword.TaskConfig{
		RawText:  *c,
		TaskType: configs.TaskTypeKeyword,
	}

	if c.DataID < 0 {
		logger.Error("dataid must >= 0")
	}

	if c.CloseInactive < 0 {
		logger.Error("close_inactive must >= 0")
	}

	// clean path
	for i, path := range c.Paths {
		c.Paths[i] = filepath.Clean(strings.TrimSpace(path))
	}

	// check exclude files
	var excludeFiles []*regexp.Regexp
	for _, patten := range c.ExcludeFiles {
		reg, err := regexp.Compile(patten)
		if err != nil {
			logger.Errorf("exclude config error, %v", err)
		}
		excludeFiles = append(excludeFiles, reg)
	}

	// 去掉相同的Label
	labelSet := make(map[string]bool, len(c.Label))
	uniqLabel := make([]configs.Label, 0, len(c.Label))
	for _, l := range c.Label {
		lid := l.Id()
		_, exists := labelSet[lid]
		if !exists {
			labelSet[l.Id()] = true
			uniqLabel = append(uniqLabel, l)
		}
	}

	taskConfig.Input = keyword.InputConfig{
		DataID:        c.DataID,
		Paths:         c.Paths,
		CloseInactive: c.CloseInactive,
		ExcludeFiles:  excludeFiles,
	}

	taskConfig.Processer = keyword.ProcessConfig{
		DataID:         c.DataID,
		Encoding:       strings.ToLower(c.Encoding),
		ScanSleep:      c.ScanSleep,
		FilterPatterns: c.FilterPatterns,
		KeywordConfigs: c.KeywordConfigs,
	}

	taskConfig.Sender = keyword.SendConfig{
		DataID:       c.DataID,
		Target:       c.Target,
		ReportPeriod: c.ReportPeriod,
		OutputFormat: c.OutputFormat,
		TimeUnit:     c.TimeUnit,
		Label:        uniqLabel,
	}

	taskConfig.TaskID = t.GetConfig().GetIdent()
	return &taskConfig
}
