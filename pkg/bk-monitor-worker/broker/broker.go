// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package broker
package broker

import (
	"context"
	"time"

	common "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	task "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
)

// Broker is a message broker interface
type Broker interface {
	// Open : open a broker connection
	Open() error
	// Close : close a broker connection
	Close() error
	// Enqueue : message -> broker
	Enqueue(ctx context.Context, msg *task.TaskMessage) error
	// EnqueueUnique : message -> broker(with uniqueId)
	EnqueueUnique(ctx context.Context, msg *task.TaskMessage, ttl time.Duration) error
	// Dequeue : message <- broker
	Dequeue(qnames ...string) (*task.TaskMessage, time.Time, error)
	// Done : finished the broker
	Done(ctx context.Context, msg *task.TaskMessage) error
	// MarkAsComplete : mark the task to complete status
	MarkAsComplete(ctx context.Context, msg *task.TaskMessage) error
	// Requeue : retry task to push the broker
	Requeue(ctx context.Context, msg *task.TaskMessage) error
	// Schedule : schedule a task in broker
	Schedule(ctx context.Context, msg *task.TaskMessage, processAt time.Time) error
	// ScheduleUnique : scheduler a task in broker(with UniqueId)
	ScheduleUnique(ctx context.Context, msg *task.TaskMessage, processAt time.Time, ttl time.Duration) error
	// Retry : moves the task from active to retry queue.
	Retry(ctx context.Context, msg *task.TaskMessage, processAt time.Time, errMsg string, isFailure bool) error
	// Archive : archive task that is finished
	Archive(ctx context.Context, msg *task.TaskMessage, errMsg string) error
	// ForwardIfReady : forward task
	ForwardIfReady(qnames ...string) error
	// DeleteExpiredCompletedTasks Task retention related method
	DeleteExpiredCompletedTasks(qname string) error
	// ListLeaseExpired Lease related methods
	ListLeaseExpired(cutoff time.Time, qnames ...string) ([]*task.TaskMessage, error)
	// ExtendLease : extends the lease for the given tasks
	ExtendLease(qname string, ids ...string) (time.Time, error)
	// WriteServerState : State snapshot related methods
	WriteServerState(info *common.ServerInfo, workers []*common.WorkerInfo, ttl time.Duration) error
	// ClearServerState : clear the server status
	ClearServerState(host string, pid int, serverID string) error
	// WriteResult writes the given result data for the specified task.
	WriteResult(qname, id string, data []byte) (n int, err error)
}
