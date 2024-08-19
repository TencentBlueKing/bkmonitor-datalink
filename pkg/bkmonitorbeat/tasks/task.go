// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"context"
	"fmt"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// BaseTask :
type BaseTask struct {
	waitgroup sync.WaitGroup

	Status       define.Status
	GlobalConfig define.Config
	TaskConfig   define.TaskConfig

	HookPreRun  utils.HookManager
	HookPostRun utils.HookManager

	s        Semaphore
	sTaskKey string // 当前任务并发限制key
	sCount   int    // 当前任务并发限制初始化执行次数
}

func (t *BaseTask) Init() { t.Status = define.TaskReady }

func (t *BaseTask) GetTaskID() int32 { return t.TaskConfig.GetTaskID() }

func (t *BaseTask) SetConfig(config define.TaskConfig) { t.TaskConfig = config }

func (t *BaseTask) GetConfig() define.TaskConfig { return t.TaskConfig }

func (t *BaseTask) SetGlobalConfig(config define.Config) { t.GlobalConfig = config }

func (t *BaseTask) GetGlobalConfig() define.Config { return t.GlobalConfig }

func (t *BaseTask) GetStatus() define.Status { return t.Status }

func (t *BaseTask) Wait() { t.waitgroup.Wait() }

func (t *BaseTask) Reload() {}

func (t *BaseTask) Stop() {}

func (t *BaseTask) GetSemaphore() Semaphore { return t.s }

func (t *BaseTask) getTaskConcurrencyLimitConfig() *configs.TaskConcurrencyLimitConfig {
	if globalConfig, ok := t.GetGlobalConfig().(*configs.Config); ok {
		taskConfig := t.GetConfig()
		if c, ok := globalConfig.ConcurrencyLimit.Task[taskConfig.GetType()]; ok {
			return c
		}
	}
	return nil
}

// PreRun :
func (t *BaseTask) PreRun(ctx context.Context) {
	conf := t.GetConfig()
	logger.Infof("[%s] task(%v) begin", conf.GetType(), conf.GetTaskID())
	t.HookPreRun.Apply(ctx)
	// 初始化并发限制对象
	c := t.getTaskConcurrencyLimitConfig()
	if c != nil {
		taskConfig := t.GetConfig()
		// 全局维度
		sInstanceKey := fmt.Sprintf("%s_per_instance", taskConfig.GetType())
		// 任务维度
		t.sTaskKey = fmt.Sprintf("%s_per_task_%s", taskConfig.GetType(), taskConfig.GetIdent())
		t.s = DefaultSemaphorePool.GetSemaphore(
			sInstanceKey, c.PerInstanceLimit,
			t.sTaskKey, c.PerTaskLimit,
		)
		// 记录并发限制初始化次数
		t.sCount++
	} else {
		// 无配置配置空对象，防止panic
		t.s = &NoopSemaphore{}
	}
	if t.Status != define.TaskRunning {
		t.Status = define.TaskRunning
	}

	t.waitgroup.Add(1)
}

// PostRun :
func (t *BaseTask) PostRun(ctx context.Context) {
	conf := t.GetConfig()
	logger.Infof("[%s] task(%v) done", conf.GetType(), conf.GetTaskID())
	t.HookPostRun.Apply(ctx)
	c := t.getTaskConcurrencyLimitConfig()
	if c != nil {
		t.sCount--
		// 当前任务无其他在使用的并发限制对象时才清理key，防止任务执行时间过长同时存在两个的情况
		if t.sCount == 0 {
			DefaultSemaphorePool.Delete(t.sTaskKey)
		}
	}
	if t.Status == define.TaskRunning {
		t.Status = define.TaskFinished
	}
	t.waitgroup.Done()
}

// EventTask 事件任务类型
type EventTask struct {
	BaseTask
	runningLock sync.Mutex
	isRunning   bool
}

// CheckRunning 检查是否正在运行,使用锁控制
func (t *EventTask) CheckRunning() bool {
	t.runningLock.Lock()
	defer t.runningLock.Unlock()
	if t.isRunning {
		return true
	}
	t.isRunning = true
	return false
}

// ResetRunningState 重置running锁
func (t *EventTask) ResetRunningState() {
	t.runningLock.Lock()
	defer t.runningLock.Unlock()
	t.isRunning = false
}

// CheckMode 检查是否为check模式
func (t *EventTask) CheckMode() bool {
	global, ok := t.GlobalConfig.(*configs.Config)
	if ok && global.Mode == "check" {
		return true
	}
	return false
}
