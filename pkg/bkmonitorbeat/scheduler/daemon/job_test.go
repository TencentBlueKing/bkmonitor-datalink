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

func TestIntervalJob(t *testing.T) {
	now := time.Now()
	job := &IntervalJob{
		task:      mock.NewTask(),
		checkTime: now,
	}
	conf := job.task.GetConfig()
	job.Init()
	job.checkTime = now

	for index := 0; index < 10; index++ {
		job.Next()
		if conf.GetPeriod() != job.checkTime.Sub(now) {
			t.Errorf("calc next time error")
		}
		now = job.checkTime
	}
}

func TestStroedIntervalJobAlignCheckTime(t *testing.T) {
	task := mock.NewTask()
	job := &StroedIntervalJob{
		IntervalJob: &IntervalJob{
			task: task,
		},
	}
	conf := task.GetConfig().(*mock.TaskConfig)
	conf.Period = time.Minute

	timeTemplate := "2006-01-02 15:04"
	seconds := make(map[int]int)
	realTime := time.Date(2018, 5, 20, 13, 14, 0, 0, time.Local)
	for taskID := 0; int32(taskID) < 2*alignCheckTimeHash; taskID++ {
		conf.TaskID = int32(taskID)
		second := 0
		for sec := 0; sec < 60; sec++ {
			job.checkTime = time.Date(2018, 5, 20, 13, 14, sec, 0, time.Local)
			job.alignCheckTime()
			if job.checkTime.Format(timeTemplate) != realTime.Format(timeTemplate) {
				t.Errorf("align time error: %v", job.checkTime)
			}
			if sec != 0 {
				if job.checkTime.Second() != second {
					t.Errorf("align task %v second not equal by %v", taskID, job.checkTime)
				}
			}
			second = job.checkTime.Second()
		}
		count, _ := seconds[second]
		seconds[second] = count + 1
	}
	if int32(len(seconds)) < alignCheckTimeHash {
		t.Errorf("hash seconds error: %v", seconds)
	}
}
