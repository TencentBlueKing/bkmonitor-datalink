// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kubeevent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

func newGather(ctx context.Context, interval time.Duration, files []string) *Gather {
	ctx, cancel := context.WithCancel(ctx)
	return &Gather{
		BaseTask: tasks.BaseTask{
			GlobalConfig: configs.NewConfig(),
			TaskConfig: configs.NewKubeEventConfig(&configs.Config{
				TaskTypeMapping: map[string]define.TaskMetaConfig{},
			}),
		},
		config: &configs.KubeEventConfig{
			Interval:  interval,
			TailFiles: files,
		},
		ctx:    ctx,
		cancel: cancel,
	}
}

func TestGatherUpdateEvent(t *testing.T) {
	f, err := os.CreateTemp("", "*.log")
	assert.NoError(t, err)

	defer func() {
		assert.NoError(t, f.Close())
		assert.NoError(t, os.Remove(f.Name()))
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g := newGather(ctx, time.Second, []string{f.Name()})
	event := `{"metadata":{"name":"bkpaas3-engine-clean-timeout-slug-pod.17036a59624316b8","namespace":"blueking","uid":"a39e0e1d-d21c-4b47-ad5a-3bb35efc0f20","resourceVersion":"65487520","creationTimestamp":"2022-07-20T03:00:19Z","managedFields":[{"manager":"kube-controller-manager","operation":"Update","apiVersion":"v1","time":"2022-07-20T03:00:19Z"}]},"reason":"SuccessfulDelete","message":"Deleted job bkpaas3-engine-clean-timeout-slug-pod-1658275200","source":{"component":"cronjob-controller"},"firstTimestamp":"2022-07-20T03:00:19Z","lastTimestamp":"2022-07-20T03:00:19Z","count":1,"type":"Normal","eventTime":null,"reportingComponent":"","reportingInstance":"","involvedObject":{"kind":"CronJob","namespace":"blueking","name":"bkpaas3-engine-clean-timeout-slug-pod","uid":"32ca7b64-8747-4c69-bcc3-9be4910d4c2f","apiVersion":"batch/v1beta1","resourceVersion":"65487518","labels":{"app.kubernetes.io/instance":"bk-paas","app.kubernetes.io/managed-by":"Helm","app.kubernetes.io/name":"engine","app.kubernetes.io/version":"1.0.0","helm.sh/chart":"engine-0.0.1"},"annotations":{"meta.helm.sh/release-name":"bk-paas","meta.helm.sh/release-namespace":"blueking"}}}
`
	ch := make(chan define.Event, 1)
	go g.Run(g.ctx, ch)

	time.Sleep(time.Second)
	g.store.started = 0
	g.store.dataID = 1001
	f.WriteString(event)

	actual := <-ch
	excepted := common.MapStr{
		"data": []common.MapStr{
			{
				"dimension": common.MapStr{
					"apiVersion": "batch/v1beta1",
					"host":       "",
					"kind":       "CronJob",
					"name":       "bkpaas3-engine-clean-timeout-slug-pod",
					"namespace":  "blueking",
					"type":       "Normal",
					"uid":        "a39e0e1d-d21c-4b47-ad5a-3bb35efc0f20",
				},
				"event": common.MapStr{
					"content": "Deleted job bkpaas3-engine-clean-timeout-slug-pod-1658275200",
					"count":   1,
				},
				"event_name": "SuccessfulDelete",
				"target":     "cronjob-controller",
				"timestamp":  int64(1658286019000),
			},
		},
		"dataid": int32(1001),
	}

	assert.Equal(t, excepted, actual.AsMapStr())
}

func TestGatherOOMEvent(t *testing.T) {
	f, err := os.CreateTemp("", "*.log")
	assert.NoError(t, err)

	defer func() {
		assert.NoError(t, f.Close())
		assert.NoError(t, os.Remove(f.Name()))
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g := newGather(ctx, time.Second, []string{f.Name()})
	event := `{"metadata":{"name":"node-9-134-111-70.170367988a3503e7","namespace":"default","uid":"af2f98af-b7d3-433b-90a2-8b89e9c9f920","resourceVersion":"65474003","creationTimestamp":"2022-07-20T02:09:50Z","managedFields":[{"manager":"kubelet","operation":"Update","apiVersion":"v1","time":"2022-07-20T02:09:50Z"}]},"reason":"SystemOOM","message":"System OOM encountered, victim process: mysqld, pid: 4020757","source":{"component":"kubelet","host":"node-9-134-111-70"},"firstTimestamp":"2022-07-20T02:09:52Z","lastTimestamp":"2022-07-20T02:09:52Z","count":1,"type":"Warning","eventTime":null,"reportingComponent":"","reportingInstance":"","involvedObject":{}}
`
	ch := make(chan define.Event, 1)
	go g.Run(g.ctx, ch)

	time.Sleep(time.Second)
	g.store.started = 0
	g.store.dataID = 1001
	f.WriteString(event)

	actual := <-ch
	excepted := common.MapStr{
		"data": []common.MapStr{
			{
				"dimension": common.MapStr{
					"apiVersion": "",
					"host":       "node-9-134-111-70",
					"kind":       "", "name": "",
					"namespace": "",
					"type":      "Warning",
					"uid":       "af2f98af-b7d3-433b-90a2-8b89e9c9f920",
				},
				"event": common.MapStr{
					"content": "System OOM encountered, victim process: mysqld, pid: 4020757",
					"count":   1,
				},
				"event_name": "SystemOOM",
				"target":     "kubelet",
				"timestamp":  int64(1658282992000),
			},
		},
		"dataid": int32(1001),
	}

	assert.Equal(t, excepted, actual.AsMapStr())
}

func TestGatherFailedSchedulingEvent(t *testing.T) {
	f, err := os.CreateTemp("", "*.log")
	assert.NoError(t, err)

	defer func() {
		assert.NoError(t, f.Close())
		assert.NoError(t, os.Remove(f.Name()))
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g := newGather(ctx, time.Second, []string{f.Name()})
	event := `{"metadata":{"name":"bkm-statefulset-worker-0.1765c80a06ff15b8","namespace":"bkmonitor-operator","selfLink":"/api/v1/namespaces/bkmonitor-operator/events/bkm-statefulset-worker-0.1765c80a06ff15b8","uid":"3890cf00-783d-4dc6-8286-366b08a71833","resourceVersion":"77837992","creationTimestamp":"2023-06-05T13:59:40Z"},"reason":"FailedScheduling","message":"0/5 nodes are available: 5 Insufficient cpu.","source":{},"firstTimestamp":null,"lastTimestamp":null,"type":"Warning","eventTime":"2023-06-05T13:59:40.912723Z","series":{"count":1,"lastObservedTime":"2023-06-05T13:59:40.953742Z"},"action":"Scheduling","reportingComponent":"default-scheduler","reportingInstance":"default-scheduler-30-49-40-191","clusterName":"","involvedObject":{"kind":"Pod","namespace":"bkmonitor-operator","name":"bkm-statefulset-worker-0","uid":"97bf28a1-ed9c-411c-a11c-9ed0538be433","apiVersion":"v1","resourceVersion":"77837990","labels":{"app.kubernetes.io/component":"bkmonitorbeat-statefulset","controller-revision-hash":"bkm-statefulset-worker-7c76d4ff7f","security.istio.io/tlsMode":"istio","service.istio.io/canonical-name":"bkm-statefulset-worker","service.istio.io/canonical-revision":"latest","statefulset.kubernetes.io/pod-name":"bkm-statefulset-worker-0","tcm.cloud.tencent.com/managed-by":"mesh-lbs8rbol"},"annotations":{"prometheus.io/path":"/stats/prometheus","prometheus.io/port":"15020","prometheus.io/scrape":"true"}}}
`
	ch := make(chan define.Event, 1)
	go g.Run(g.ctx, ch)

	time.Sleep(time.Second)
	g.store.started = 0
	g.store.dataID = 1001
	f.WriteString(event)

	actual := <-ch
	excepted := common.MapStr{
		"data": []common.MapStr{
			{
				"dimension": common.MapStr{
					"apiVersion": "v1",
					"host":       "",
					"kind":       "Pod",
					"name":       "bkm-statefulset-worker-0",
					"namespace":  "bkmonitor-operator",
					"type":       "Warning",
					"uid":        "3890cf00-783d-4dc6-8286-366b08a71833",
				},
				"event": common.MapStr{
					"content": "0/5 nodes are available: 5 Insufficient cpu.",
					"count":   1,
				},
				"event_name": "FailedScheduling",
				"target":     "kubelet",
				"timestamp":  int64(1685973580000),
			},
		},
		"dataid": int32(1001),
	}

	assert.Equal(t, excepted, actual.AsMapStr())
}
