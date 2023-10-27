// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

// Task 代表着调度单元
type Task interface {
	// PipelineName 任务所属流水线名称
	PipelineName() string

	// Record 返回任务处理的数据
	Record() *Record

	// StageCount 返回任务正在处理的步骤索引
	StageCount() int

	// StageAt 返回第 i 个处理步骤的处理器
	StageAt(i int) string
}

type task struct {
	pipelineName string
	processors   []string
	record       *Record
}

// NewTask 生成新的 Task 实例
func NewTask(record *Record, pipelineName string, processors []string) Task {
	return &task{
		pipelineName: pipelineName,
		processors:   processors,
		record:       record,
	}
}

// PipelineName 实现 Task PipelineName 方法
func (t *task) PipelineName() string {
	return t.pipelineName
}

// Record 实现 Task Record 方法
func (t *task) Record() *Record {
	return t.record
}

// StageCount 实现 Task StageCount 方法
func (t *task) StageCount() int {
	return len(t.processors)
}

// StageAt 实现 Task StageAt 方法
func (t *task) StageAt(i int) string {
	if i < len(t.processors) {
		return t.processors[i]
	}
	return ""
}

type TaskQueue struct {
	tasks chan Task
	mode  PushMode
}

// NewTaskQueue 生成 Tasks 消息队列
func NewTaskQueue(mode PushMode) *TaskQueue {
	return &TaskQueue{
		mode:  mode,
		tasks: make(chan Task, Concurrency()*QueueAmplification),
	}
}

func (q *TaskQueue) Push(r Task) {
	switch q.mode {
	case PushModeGuarantee:
		q.tasks <- r
	case PushModeDropIfFull:
		select {
		case q.tasks <- r:
		default:
		}
	}
}

func (q *TaskQueue) Get() <-chan Task {
	return q.tasks
}
