// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"runtime"
	"time"
)

const (
	QueueAmplification = 2
	AcquireTimeout     = 5 * time.Second
)

var coreNums = runtime.NumCPU()

func Concurrency() int {
	return coreNums * 2
}

func CoreNum() int {
	return coreNums
}

func SetCoreNum(n int) {
	if n <= 0 {
		// Go 1.25 默认会按 cgroup 配额计算 GOMAXPROCS。worker 数量和队列容量按 CoreNum 固定初始化，
		// 因此未显式配置时取当前有效并发数，并固定下来，避免运行时调度器与内部并发规模不一致。
		n = runtime.GOMAXPROCS(0)
	}
	coreNums = n
	runtime.GOMAXPROCS(n)
}
