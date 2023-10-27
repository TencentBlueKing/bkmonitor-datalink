// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package broker

import (
	"context"
	"time"

	common "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	task "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
)

// Broker is a message broker interface
type Broker interface {
	// base func
	Open() error
	Close() error
	Enqueue(ctx context.Context, msg *task.TaskMessage) error
	EnqueueUnique(ctx context.Context, msg *task.TaskMessage, ttl time.Duration) error
	Dequeue(qnames ...string) (*task.TaskMessage, time.Time, error)
	Done(ctx context.Context, msg *task.TaskMessage) error
	MarkAsComplete(ctx context.Context, msg *task.TaskMessage) error
	Requeue(ctx context.Context, msg *task.TaskMessage) error
	Schedule(ctx context.Context, msg *task.TaskMessage, processAt time.Time) error
	ScheduleUnique(ctx context.Context, msg *task.TaskMessage, processAt time.Time, ttl time.Duration) error
	Retry(ctx context.Context, msg *task.TaskMessage, processAt time.Time, errMsg string, isFailure bool) error
	Archive(ctx context.Context, msg *task.TaskMessage, errMsg string) error
	ForwardIfReady(qnames ...string) error

	// Task retention related method
	DeleteExpiredCompletedTasks(qname string) error

	// Lease related methods
	ListLeaseExpired(cutoff time.Time, qnames ...string) ([]*task.TaskMessage, error)
	ExtendLease(qname string, ids ...string) (time.Time, error)

	// State snapshot related methods
	WriteServerState(info *common.ServerInfo, workers []*common.WorkerInfo, ttl time.Duration) error
	ClearServerState(host string, pid int, serverID string) error

	WriteResult(qname, id string, data []byte) (n int, err error)
}
