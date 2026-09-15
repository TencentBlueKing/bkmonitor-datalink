// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package taskgroup 监督一组有界的长生命周期任务，并统一处理失败取消和退出等待。
package taskgroup

import (
	"context"
	"errors"
	"fmt"
)

// Task 是由同一父进程监督的长生命周期任务。
type Task struct {
	Name string
	Run  func(context.Context) error
}

type result struct {
	name                string
	err                 error
	contextWasCancelled bool
}

// Run 并发运行全部任务。任一任务异常会取消其余任务，并等待所有任务完成清理后返回。
func Run(ctx context.Context, tasks []Task) error {
	if ctx == nil {
		return fmt.Errorf("run task group: context must not be nil")
	}
	if len(tasks) == 0 {
		return fmt.Errorf("run task group: tasks must not be empty")
	}
	for index, task := range tasks {
		if task.Name == "" || task.Run == nil {
			return fmt.Errorf("run task group: task[%d] must have name and runner", index)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, len(tasks))
	for _, task := range tasks {
		go func(task Task) {
			err := call(runCtx, task)
			results <- result{
				name:                task.Name,
				err:                 err,
				contextWasCancelled: runCtx.Err() != nil,
			}
		}(task)
	}

	var runErrors []error
	for range tasks {
		result := <-results
		taskErr := result.err
		if taskErr == nil && !result.contextWasCancelled {
			taskErr = fmt.Errorf("task stopped unexpectedly")
		}
		if taskErr == nil || expectedCancellation(taskErr, runCtx) {
			continue
		}
		runErrors = append(runErrors, fmt.Errorf("task %q: %w", result.name, taskErr))
		cancel()
	}
	return errors.Join(runErrors...)
}

func call(ctx context.Context, task Task) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("task panic: %v", recovered)
		}
	}()
	return task.Run(ctx)
}

func expectedCancellation(err error, ctx context.Context) bool {
	return ctx.Err() != nil && errors.Is(err, ctx.Err())
}
