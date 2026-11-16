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
	"context"
	"testing"
	"time"

	"github.com/elastic/go-ucfg/yaml"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/script"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/test/mock"
)

func TestReloadKeepsUnchangedScriptJob(t *testing.T) {
	root := configs.NewConfig()
	load := func(env string) define.Task {
		cfg, err := yaml.NewConfig([]byte(`type: script
name: test
version: "1.0"
dataid: 1
tasks:
  - task_id: 1
    period: 60s
    command: test-script
    user_env: ` + env))
		require.NoError(t, err)
		meta := configs.NewScriptTaskMetaConfig(root)
		child := &configs.ChildTaskMetaConfig{TaskMetaConfig: meta}
		require.NoError(t, cfg.Unpack(child))
		require.NoError(t, child.Clean())
		return script.New(root, meta.Tasks[0])
	}

	s := &Daemon{daemonState: newDaemonState()}
	s.ctx = context.Background()
	job := &IntervalJob{task: load("{A: one, B: two, C: three}")}
	job.SetScheduler(s)
	defer job.cancel()
	s.jobs.Push(job)
	ident := job.task.GetConfig().GetIdent()
	for i := 0; i < 32; i++ {
		task := load("{C: three, A: one, B: two}")
		require.Equal(t, ident, task.GetConfig().GetIdent())
		require.NoError(t, s.Reload(s.ctx, root, []define.Task{task}))
		require.Same(t, job, s.jobs.First())
		require.NoError(t, job.ctx.Err())
	}
	require.NotEqual(t, ident, load("{A: one, B: changed, C: three}").GetConfig().GetIdent())
	require.NotEqual(t, ident, load("{A: one, B: two}").GetConfig().GetIdent())
}

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
