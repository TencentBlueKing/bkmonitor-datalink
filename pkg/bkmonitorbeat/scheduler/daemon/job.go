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
	"fmt"
	"time"

	"github.com/emirpasic/gods/utils"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var timeTemplate = "2006-01-02T15:04:05Z07:00"

// Job :
type Job interface {
	GetCheckTime() time.Time
	Init()
	Next()
	Run(e chan<- define.Event)
	GetTask() define.Task
	Reload()
	Stop()
	SetScheduler(scheduler define.Scheduler)
}

// NewJob :
func NewJob(task define.Task, scheduler define.Scheduler) Job {
	job := &StroedIntervalJob{
		IntervalJob: &IntervalJob{
			task: task,
		},
	}
	job.SetScheduler(scheduler)
	return job
}

// IntervalJob :
type IntervalJob struct {
	ctx       context.Context
	cancel    context.CancelFunc
	scheduler *Daemon
	task      define.Task
	checkTime time.Time
}

// Init :
func (j *IntervalJob) Init() {
	j.checkTime = time.Now()
}

// Next :
func (j *IntervalJob) Next() {
	conf := j.task.GetConfig()
	j.checkTime = j.checkTime.Add(conf.GetPeriod())
}

// Reload :
func (j *IntervalJob) Reload() {
	logger.Info("IntervalJob.Reload")
	j.task.Reload()
}

// Stop :
func (j *IntervalJob) Stop() {
	logger.Info("IntervalJob.Stop")
	if j.task != nil {
		logger.Infof("job stop,task id:%v", j.task.GetTaskID())
	}
	j.cancel()
	j.task.Stop()
}

// SetScheduler :
func (j *IntervalJob) SetScheduler(s define.Scheduler) {
	scheduler := s.(*Daemon)
	j.scheduler = scheduler

	j.ctx, j.cancel = context.WithCancel(scheduler.ctx)
}

// Run :
func (j *IntervalJob) Run(e chan<- define.Event) {
	j.task.Run(j.ctx, e)
}

// GetCheckTime :
func (j *IntervalJob) GetCheckTime() time.Time {
	return j.checkTime
}

// GetTask :
func (j *IntervalJob) GetTask() define.Task {
	return j.task
}

// JobTimeComparator :
func JobTimeComparator(job1 interface{}, job2 interface{}) int {
	return utils.TimeComparator(job1.(Job).GetCheckTime(), job2.(Job).GetCheckTime())
}

// StroedIntervalJob :
type StroedIntervalJob struct {
	*IntervalJob
	taskTimeKey string
}

func (j *StroedIntervalJob) initStoredCheckTime() bool {
	now := time.Now()
	taskConf := j.task.GetConfig()
	period := taskConf.GetPeriod()
	tstr, err := storage.Get(j.taskTimeKey)
	if err != nil {
		return false
	}

	lastTime, err := time.Parse(timeTemplate, tstr)
	if err != nil {
		return false
	}

	if now.Before(lastTime) {
		return false
	}

	if now.Sub(lastTime) > period {
		return false
	}
	logger.Debugf("%v loaded stored time: %v", j.taskTimeKey, lastTime)
	j.checkTime = lastTime
	return true
}

var alignCheckTimeHash int32 = 30

func (j *StroedIntervalJob) alignCheckTime() {
	taskConf := j.task.GetConfig()
	period := taskConf.GetPeriod()
	taskID := taskConf.GetTaskID()
	checkTime := j.checkTime.Truncate(period)    // 对齐上一个时刻
	second := taskID + int32(checkTime.Second()) // 打散
	j.checkTime = checkTime.Add(time.Duration(second%alignCheckTimeHash) * time.Second)
}

// Init :
func (j *StroedIntervalJob) Init() {
	j.IntervalJob.Init()

	taskConf := j.task.GetConfig()
	j.taskTimeKey = fmt.Sprintf("task_%v_%v_check_time", taskConf.GetType(), taskConf.GetTaskID())

	if j.initStoredCheckTime() {
		j.IntervalJob.Next()
	}
	j.alignCheckTime()
	logger.Debugf("%v check time: %v", j.taskTimeKey, j.checkTime)
}

// Next :
func (j *StroedIntervalJob) Next() {
	taskConf := j.task.GetConfig()
	tstr := j.checkTime.Format(timeTemplate)
	logger.Debugf("save check time: %v", tstr)
	err := storage.Set(j.taskTimeKey, tstr, 2*taskConf.GetPeriod())
	if err != nil {
		logger.Debugf("store check time failed: %v", err)
	}
	j.IntervalJob.Next()
}
