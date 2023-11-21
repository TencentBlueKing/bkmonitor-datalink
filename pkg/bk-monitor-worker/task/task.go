// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
)

type Task struct {
	Kind    string
	Payload []byte
	Options []Option
}

type SerializerTask struct {
	Kind    string
	Payload []byte
	Options Options
}

// NewTask make a task
func NewTask(kind string, payload []byte, opts ...Option) *Task {
	return &Task{
		Kind:    kind,
		Payload: payload,
		Options: opts,
	}
}

func NewSerializerTask(t Task) (*SerializerTask, error) {
	options, err := ComposeOptions(t.Options...)
	if err != nil {
		return nil, err
	}

	return &SerializerTask{
		Kind:    t.Kind,
		Payload: t.Payload,
		Options: options,
	}, nil
}

// TaskInfo task detail
type TaskInfo struct {
	ID            string
	Queue         string
	Kind          string
	Payload       []byte
	State         TaskState
	MaxRetry      int
	Retried       int
	LastErr       string
	LastFailedAt  time.Time
	Timeout       time.Duration
	Deadline      time.Time
	NextProcessAt time.Time
	Retention     time.Duration
	CompletedAt   time.Time
	Result        []byte
}

func NewTaskInfo(msg *TaskMessage, state TaskState, nextProcessAt time.Time, result []byte) *TaskInfo {
	info := TaskInfo{
		ID:            msg.ID,
		Queue:         msg.Queue,
		Kind:          msg.Kind,
		Payload:       msg.Payload,
		MaxRetry:      msg.Retry,
		Retried:       msg.Retried,
		LastErr:       msg.ErrorMsg,
		Timeout:       time.Duration(msg.Timeout) * time.Second,
		Deadline:      timex.UnixTime2Time(msg.Deadline),
		Retention:     time.Duration(msg.Retention) * time.Second,
		NextProcessAt: nextProcessAt,
		LastFailedAt:  timex.UnixTime2Time(msg.LastFailedAt),
		CompletedAt:   timex.UnixTime2Time(msg.CompletedAt),
		Result:        result,
	}

	switch state {
	case TaskStateActive:
		info.State = TaskStateActive
	case TaskStatePending:
		info.State = TaskStatePending
	case TaskStateScheduled:
		info.State = TaskStateScheduled
	case TaskStateRetry:
		info.State = TaskStateRetry
	case TaskStateArchived:
		info.State = TaskStateArchived
	case TaskStateCompleted:
		info.State = TaskStateCompleted
	default:
		panic(fmt.Sprintf("unknown state: %d", state))
	}
	return &info
}

type TaskMessage struct {
	Kind         string
	Payload      []byte
	ID           string
	Queue        string
	Retry        int
	Retried      int
	ErrorMsg     string
	LastFailedAt int64
	Timeout      int64
	Deadline     int64
	UniqueKey    string
	Retention    int64
	CompletedAt  int64
}

type TaskMetadata struct {
	id         string
	maxRetry   int
	retryCount int
	qname      string
}
