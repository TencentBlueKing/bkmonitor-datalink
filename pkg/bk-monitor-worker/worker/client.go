// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker"
	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Client struct {
	broker broker.Broker
}

var clientInstance *Client

// GetClient new a client
func GetClient() (*Client, error) {
	if clientInstance != nil {
		return clientInstance, nil
	}

	brokerInstance := rdb.GetRDB()
	clientInstance = &Client{broker: brokerInstance}
	return clientInstance, nil
}

// Close close the broker
func (c *Client) Close() error {
	return c.broker.Close()
}

// Enqueue 入队列
func (c *Client) Enqueue(task *t.Task, opts ...t.Option) (*t.TaskInfo, error) {
	metrics.EnqueueTaskTotal(task.Kind)
	return c.EnqueueWithContext(context.Background(), task, opts...)
}

// EnqueueWithContext this method is the processing logic for messages entering the queue.
func (c *Client) EnqueueWithContext(ctx context.Context, task *t.Task, opts ...t.Option) (*t.TaskInfo, error) {
	if task == nil {
		return nil, fmt.Errorf("task cannot be nil")
	}
	if stringx.IsEmpty(task.Kind) {
		return nil, fmt.Errorf("task typename cannot be empty")
	}
	// merge task options with the options provided at enqueue time.
	opts = append(task.Options, opts...)
	opt, err := t.ComposeOptions(opts...)
	if err != nil {
		return nil, err
	}
	// check params
	deadline := common.NotDeadline
	if !opt.Deadline.IsZero() {
		deadline = opt.Deadline
	}
	timeout := common.NotTimeout
	if opt.Timeout != 0 {
		timeout = opt.Timeout
	}
	if deadline.Equal(common.NotDeadline) && timeout == common.NotTimeout {
		// If neither deadline nor timeout are set, use default timeout.
		timeout = common.DefaultTimeout
	}
	var uniqueKey string
	if opt.UniqueTTL > 0 {
		uniqueKey = common.UniqueKey(opt.Queue, task.Kind, task.Payload)
	}
	// 组装任务消息
	msg := &t.TaskMessage{
		ID:        opt.TaskID,
		Kind:      task.Kind,
		Payload:   task.Payload,
		Queue:     opt.Queue,
		Retry:     opt.Retry,
		Deadline:  deadline.Unix(),
		Timeout:   int64(timeout.Seconds()),
		UniqueKey: uniqueKey,
		Retention: int64(opt.Retention.Seconds()),
	}
	now := time.Now()
	var state t.TaskState
	if opt.ProcessAt.After(now) {
		err = c.schedule(ctx, msg, opt.ProcessAt, opt.UniqueTTL)
		state = t.TaskStateScheduled
	} else {
		opt.ProcessAt = now
		err = c.enqueue(ctx, msg, opt.UniqueTTL)
		state = t.TaskStatePending
	}
	switch {
	case errors.Is(err, errors.ErrDuplicateTask):
		logger.Warnf("task: %s already exists, not schedule a task again, error: %+v", task.Kind, err)
		return nil, fmt.Errorf("task already exists")
	case errors.Is(err, errors.ErrTaskIdConflict):
		logger.Warnf("task: %s conflict with exist task, not schedule a task again, error: %+v", task.Kind, err)
		return nil, fmt.Errorf("task conflict with exist task")
	case err != nil:
		logger.Errorf("task: %s is error, not schedule a task again, error: %+v", task.Kind, err)
		return nil, err
	}
	return t.NewTaskInfo(msg, state, opt.ProcessAt, nil), nil
}

// enqueue 根据类型判断进入的队列
func (c *Client) enqueue(ctx context.Context, msg *t.TaskMessage, uniqueTTL time.Duration) error {
	if uniqueTTL > 0 {
		return c.broker.EnqueueUnique(ctx, msg, uniqueTTL)
	}
	return c.broker.Enqueue(ctx, msg)
}

// schedule
func (c *Client) schedule(ctx context.Context, msg *t.TaskMessage, t time.Time, uniqueTTL time.Duration) error {
	if uniqueTTL > 0 {
		ttl := t.Add(uniqueTTL).Sub(time.Now())
		return c.broker.ScheduleUnique(ctx, msg, t, ttl)
	}
	return c.broker.Schedule(ctx, msg, t)
}
