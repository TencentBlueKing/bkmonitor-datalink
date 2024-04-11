// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"context"

	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
)

// Handler task handler interface
type Handler interface {
	ProcessTask(context.Context, *t.Task) error
}

type HandlerFunc func(context.Context, *t.Task) error

// ProcessTask calls fn(ctx, task)
func (fn HandlerFunc) ProcessTask(ctx context.Context, task *t.Task) error {
	return fn(ctx, task)
}

// ErrorHandler error task handler
type ErrorHandler interface {
	HandleError(ctx context.Context, task *t.Task, err error)
}

type ErrorHandlerFunc func(ctx context.Context, task *t.Task, err error)

// HandleError calls fn(ctx, task, err)
func (fn ErrorHandlerFunc) HandleError(ctx context.Context, task *t.Task, err error) {
	fn(ctx, task, err)
}
