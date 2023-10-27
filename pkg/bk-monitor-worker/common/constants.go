// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

import "time"

// 默认队列名
const (
	// 默认前缀
	DefaultQueuePrefix = "bmw"
	// 默认队列名
	DefaultQueueName = "bmw:default"
)

// DefaultQueue is the redis key for the default queue.
var DefaultQueue = PendingKey(DefaultQueueName)

// key 标识
const (
	AllServers    = "bmw:servers"
	AllWorkers    = "bmw:workers"
	AllSchedulers = "bmw:schedulers"
	AllQueues     = "bmw:queues"
	CancelChannel = "bmw:cancel"
)

const (
	// 默认最大重试次数
	DefaultMaxRetry = 10
	// 默认超时时间
	DefaultTimeout = 30 * time.Minute
	// 默认等待时间
	DefaultShutdownTimeout = 8 * time.Second
	// 默认健康检查间隔
	DefaultHealthCheckInterval = 15 * time.Second
	// 默认延迟任务检测间隔
	DefaultDelayedTaskCheckInterval = 5 * time.Second
)

var (
	// 无超时
	NotTimeout time.Duration = 0
	// 无截止时间
	NotDeadline time.Time = time.Unix(0, 0)
)

const (
	Success = 0
	// 参数异常
	ParamsError  = 400
	UnknownError = 2001400
)
