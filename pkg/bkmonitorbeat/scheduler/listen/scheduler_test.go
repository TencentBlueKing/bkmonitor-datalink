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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

type reloadTestConfig struct {
	gatherUpDataID int32
}

func (c *reloadTestConfig) GetTaskConfigListByType(string) []define.TaskConfig { return nil }
func (c *reloadTestConfig) GetGatherUpDataID() int32                           { return c.gatherUpDataID }
func (c *reloadTestConfig) Clean() error                                       { return nil }

type reloadTestTask struct {
	tasks.BaseTask
}

func (t *reloadTestTask) Run(context.Context, chan<- define.Event) {}

func newReloadTestTask(ident string, gatherUpDataID int32) define.Task {
	return newReloadTestTaskWithType(ident, configs.ConfigTypeMetric, gatherUpDataID)
}

type reloadTestTaskConfig struct {
	*configs.MetricBeatConfig
	taskType string
}

func (c *reloadTestTaskConfig) GetType() string { return c.taskType }

func newReloadTestTaskWithType(ident, taskType string, gatherUpDataID int32) define.Task {
	taskConfig := &reloadTestTaskConfig{
		MetricBeatConfig: configs.NewMetricBeatConfig(),
		taskType:         taskType,
	}
	taskConfig.SetIdent(ident)
	return &reloadTestTask{BaseTask: tasks.BaseTask{
		GlobalConfig: &reloadTestConfig{gatherUpDataID: gatherUpDataID},
		TaskConfig:   taskConfig,
	}}
}

func TestPlanReloadReplacesTaskWhenGatherUpDataIDChanges(t *testing.T) {
	currentTask := newReloadTestTask("metricbeat", 100)
	newTask := newReloadTestTask("metricbeat", 200)

	deleteList, addList := planReloadTasks(
		map[string]define.Task{"metricbeat": currentTask},
		[]define.Task{newTask},
	)

	if len(deleteList) != 1 || deleteList[0] != "metricbeat" {
		t.Fatalf("delete list = %v, want [metricbeat]", deleteList)
	}
	if len(addList) != 1 || addList[0] != newTask {
		t.Fatalf("add list = %v, want the task with the new gather-up data ID", addList)
	}
}

func TestPlanReloadReplacesOnlyMetricbeatWhenGatherUpDataIDChanges(t *testing.T) {
	taskTypes := []string{
		configs.ConfigTypeMetric,
		configs.ConfigTypeTrap,
		configs.ConfigTypeKubeevent,
		configs.ConfigTypeDmesg,
	}
	currentTasks := make(map[string]define.Task, len(taskTypes))
	newTasks := make([]define.Task, 0, len(taskTypes))
	for _, taskType := range taskTypes {
		currentTasks[taskType] = newReloadTestTaskWithType(taskType, taskType, 100)
		newTasks = append(newTasks, newReloadTestTaskWithType(taskType, taskType, 200))
	}

	deleteList, addList := planReloadTasks(currentTasks, newTasks)

	if len(deleteList) != 1 || deleteList[0] != configs.ConfigTypeMetric {
		t.Fatalf("delete list = %v, want [%s]", deleteList, configs.ConfigTypeMetric)
	}
	if len(addList) != 1 {
		t.Fatalf("add list count = %d, want 1", len(addList))
	}
	if got := addList[0].GetConfig().GetType(); got != configs.ConfigTypeMetric {
		t.Fatalf("added task type = %s, want %s", got, configs.ConfigTypeMetric)
	}
}

func TestPlanReloadKeepsTaskWhenGatherUpDataIDIsUnchanged(t *testing.T) {
	currentTask := newReloadTestTask("metricbeat", 100)
	newTask := newReloadTestTask("metricbeat", 100)

	deleteList, addList := planReloadTasks(
		map[string]define.Task{"metricbeat": currentTask},
		[]define.Task{newTask},
	)

	if len(deleteList) != 0 {
		t.Fatalf("delete list = %v, want no replacement", deleteList)
	}
	if len(addList) != 0 {
		t.Fatalf("add list = %v, want no replacement", addList)
	}
}
