// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"sync"
)

// Task
type Task struct {
	Handler func(v ...any)
	Params  []any
}

// NewTask
func NewTask(handler func(v ...any), params ...any) *Task {
	return &Task{
		Handler: handler,
		Params:  params,
	}
}

// Pool
type Pool struct {
	cap   int
	tasks []*Task
	wg    *sync.WaitGroup
}

// NewPool
func NewPool(cap int) *Pool {
	return &Pool{cap: cap, wg: new(sync.WaitGroup)}
}

// Put
func (p *Pool) Put(t *Task) {
	p.tasks = append(p.tasks, t)
}

// Run
func (p *Pool) Run() {
	p.wg.Add(len(p.tasks))

	var taskCh chan *Task
	if p.cap < 0 {
		taskCh = make(chan *Task, len(p.tasks))
	} else {
		taskCh = make(chan *Task, p.cap)
	}

	// 生产
	go func() {
		for _, task := range p.tasks {
			taskCh <- task
		}
		close(taskCh)
	}()

	// 消费
	for task := range taskCh {
		go func(task *Task) {
			defer p.wg.Done()
			task.Handler(task.Params...)
		}(task)
	}
}

// Wait
func (p *Pool) Wait() {
	p.wg.Wait()
}

// Clean
func (p *Pool) Clean() {
	p.wg.Wait()
	p.tasks = nil
}
