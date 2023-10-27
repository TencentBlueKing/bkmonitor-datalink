// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package daemon

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/test/mock"
)

func newJobWithPeriod(period time.Duration) Job {
	task := mock.NewTask()
	conf := task.GetConfig().(*mock.TaskConfig)
	conf.Period = period
	job := IntervalJob{
		task: task,
	}
	job.Init()
	job.checkTime = time.Now()
	job.Next()
	return &job
}

func TestLockQueue(t *testing.T) {
	queue := NewLockQueue()
	j1 := newJobWithPeriod(time.Minute)
	j2 := newJobWithPeriod(40 * time.Second)
	j3 := newJobWithPeriod(50 * time.Second)
	queue.Push(j1, j2, j3)

	if queue.First() != j2 {
		t.Errorf("queue sort error")
	}

	j4 := queue.Pop()
	if j4 != j2 {
		t.Errorf("queue pop error")
	}

	j4.Next()
	queue.Push(j4)

	jobs := queue.PopUntil(j1.GetCheckTime())
	if len(jobs) != 2 || jobs[0] != j3 || jobs[1] != j1 {
		t.Errorf("queue pop until error")
	}

	queue.Clear()
	if queue.First() != nil {
		t.Errorf("queue clear error")
	}
}
