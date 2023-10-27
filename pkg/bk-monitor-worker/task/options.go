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

	"github.com/google/uuid"

	common "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
)

type OptionType int

const (
	MaxRetryOpt OptionType = iota
	QueueOpt
	TimeoutOpt
	DeadlineOpt
	UniqueOpt
	ProcessAtOpt
	ProcessIntervalOpt
	TaskIDOpt
	RetentionOpt
)

// Option specifies the task processing behavior.
type Option interface {
	String() string
	Type() OptionType
	Value() interface{}
}

// Internal option representations.
type (
	retryOption           int
	queueOption           string
	taskIDOption          string
	timeoutOption         time.Duration
	deadlineOption        time.Time
	uniqueOption          time.Duration
	processAtOption       time.Time
	processIntervalOption time.Duration
	retentionOption       time.Duration
)

// MaxRetry returns an option to specify the max number of times
// the task will be retried.
//
// Negative retry count is treated as zero retry.
func MaxRetry(n int) Option {
	if n < 0 {
		n = 0
	}
	return retryOption(n)
}

func (n retryOption) String() string { return fmt.Sprintf("MaxRetry(%d)", int(n)) }

func (n retryOption) Type() OptionType { return MaxRetryOpt }

func (n retryOption) Value() interface{} { return int(n) }

// Queue returns an option to specify the queue to enqueue the task into.
func Queue(name string) Option {
	return queueOption(name)
}

func (name queueOption) String() string { return fmt.Sprintf("Queue(%q)", string(name)) }

func (name queueOption) Type() OptionType { return QueueOpt }

func (name queueOption) Value() interface{} { return string(name) }

// TaskID returns an option to specify the task ID.
func TaskID(id string) Option {
	return taskIDOption(id)
}

func (id taskIDOption) String() string { return fmt.Sprintf("TaskID(%q)", string(id)) }

func (id taskIDOption) Type() OptionType { return TaskIDOpt }

func (id taskIDOption) Value() interface{} { return string(id) }

// Timeout returns an option to specify how long a task may run.
func Timeout(d time.Duration) Option {
	return timeoutOption(d)
}

func (d timeoutOption) String() string { return fmt.Sprintf("Timeout(%v)", time.Duration(d)) }

func (d timeoutOption) Type() OptionType { return TimeoutOpt }

func (d timeoutOption) Value() interface{} { return time.Duration(d) }

// Deadline returns an option to specify the deadline for the given task.
func Deadline(t time.Time) Option {
	return deadlineOption(t)
}

func (t deadlineOption) String() string {
	return fmt.Sprintf("Deadline(%v)", time.Time(t).Format(time.UnixDate))
}

func (t deadlineOption) Type() OptionType { return DeadlineOpt }

func (t deadlineOption) Value() interface{} { return time.Time(t) }

// Unique returns an option to enqueue a task only if the given task is unique.
func Unique(ttl time.Duration) Option {
	return uniqueOption(ttl)
}

func (ttl uniqueOption) String() string { return fmt.Sprintf("Unique(%v)", time.Duration(ttl)) }

func (ttl uniqueOption) Type() OptionType { return UniqueOpt }

func (ttl uniqueOption) Value() interface{} { return time.Duration(ttl) }

// ProcessAt returns an option to specify when to process the given task.
//
// If there's a conflicting ProcessInterval option, the last option passed to Enqueue overrides the others.
func ProcessAt(t time.Time) Option {
	return processAtOption(t)
}

func (t processAtOption) String() string {
	return fmt.Sprintf("ProcessAt(%v)", time.Time(t).Format(time.UnixDate))
}

func (t processAtOption) Type() OptionType { return ProcessAtOpt }

func (t processAtOption) Value() interface{} { return time.Time(t) }

// ProcessInterval returns an option to specify when to process the given task relative to the current time.
//
// If there's a conflicting ProcessAt option, the last option passed to Enqueue overrides the others.
func ProcessInterval(d time.Duration) Option {
	return processIntervalOption(d)
}

func (d processIntervalOption) String() string {
	return fmt.Sprintf("ProcessInterval(%v)", time.Duration(d))
}

func (d processIntervalOption) Type() OptionType { return ProcessIntervalOpt }

func (d processIntervalOption) Value() interface{} { return time.Duration(d) }

// Retention returns an option to specify the duration of retention period for the task.
func Retention(d time.Duration) Option {
	return retentionOption(d)
}

func (ttl retentionOption) String() string { return fmt.Sprintf("Retention(%v)", time.Duration(ttl)) }

func (ttl retentionOption) Type() OptionType { return RetentionOpt }

func (ttl retentionOption) Value() interface{} { return time.Duration(ttl) }

// ErrDuplicateTask indicates that the given task could not be enqueued since it's a duplicate of another task.
//
// ErrDuplicateTask error only applies to tasks enqueued with a Unique option.
var ErrDuplicateTask = errors.New("task already exists")

// ErrTaskIDConflict indicates that the given task could not be enqueued since its task ID already exists.
//
// ErrTaskIDConflict error only applies to tasks enqueued with a TaskID option.
var ErrTaskIDConflict = errors.New("task ID conflicts with another task")

type option struct {
	Retry     int
	Queue     string
	TaskID    string
	Timeout   time.Duration
	Deadline  time.Time
	UniqueTTL time.Duration
	ProcessAt time.Time
	Retention time.Duration
}

// ComposeOptions compose option with custom options
func ComposeOptions(opts ...Option) (option, error) {
	// 默认 option
	res := option{
		Retry:     common.DefaultMaxRetry,
		Queue:     common.DefaultQueueName,
		TaskID:    uuid.NewString(),
		Timeout:   0,
		Deadline:  time.Time{},
		ProcessAt: time.Now(),
	}
	for _, opt := range opts {
		switch opt := opt.(type) {
		case retryOption:
			res.Retry = int(opt)
		case queueOption:
			qname := string(opt)
			if err := common.ValidateQueueName(qname); err != nil {
				return option{}, err
			}
			res.Queue = qname
		case taskIDOption:
			id := string(opt)
			if stringx.IsEmpty(id) {
				return option{}, errors.New("task ID cannot be empty")
			}
			res.TaskID = id
		case timeoutOption:
			res.Timeout = time.Duration(opt)
		case deadlineOption:
			res.Deadline = time.Time(opt)
		case uniqueOption:
			ttl := time.Duration(opt)
			if ttl < 1*time.Second {
				return option{}, errors.New("Unique TTL cannot be less than 1s")
			}
			res.UniqueTTL = ttl
		case processAtOption:
			res.ProcessAt = time.Time(opt)
		case processIntervalOption:
			res.ProcessAt = time.Now().Add(time.Duration(opt))
		case retentionOption:
			res.Retention = time.Duration(opt)
		default:
			// ignore unexpected option
		}
	}
	return res, nil
}
