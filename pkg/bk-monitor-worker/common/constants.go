// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

import (
	"time"
)

const (
	// DefaultQueueName 默认队列名
	DefaultQueueName = "default"
)

// DefaultQueue is the redis key for the default queue.
var DefaultQueue = PendingKey(DefaultQueueName)

// key 标识
const (
	// AllServers server key
	AllServers = "bmw:servers"
	// AllWorkers worker key
	AllWorkers = "bmw:workers"
	// AllSchedulers scheduler key
	AllSchedulers = "bmw:schedulers"
	// AllQueues queues key
	AllQueues = "bmw:queues"
)

const (
	// DefaultMaxRetry 默认最大重试次数
	DefaultMaxRetry = 10
	// DefaultTimeout 默认超时时间
	DefaultTimeout = 30 * time.Minute
	// DefaultShutdownTimeout 默认等待时间
	DefaultShutdownTimeout = 8 * time.Second
	// DefaultHealthCheckInterval 默认健康检查间隔
	DefaultHealthCheckInterval = 15 * time.Second
	// DefaultDelayedTaskCheckInterval 默认延迟任务检测间隔
	DefaultDelayedTaskCheckInterval = 5 * time.Second
)

var (
	// NotTimeout 无超时
	NotTimeout time.Duration = 0
	// NotDeadline 无截止时间
	NotDeadline time.Time = time.Unix(0, 0)
)

const (
	// Success 成功
	Success = 0
	// ParamsError 参数异常错误
	ParamsError = 400
)
