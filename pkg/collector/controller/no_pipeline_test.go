// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metrictest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func noPipeline(t *testing.T, protocol define.RequestType) float64 {
	t.Helper()
	v, _ := metrictest.Value(t, "bk_collector_receiver_rejected_total", map[string]string{"protocol": protocol.S(), "reason": "no_pipeline"})
	return v
}

// A record that comes in from the receiver when no pipeline for its type
// exists is dropped and counted once as no_pipeline under the protocol it
// came by; a record whose pipeline exists becomes a task and is not counted.
func TestARecordWithNoPipelineIsCounted(t *testing.T) {
	mgr, err := pipeline.New(confengine.MustLoadConfigContent(`
apm:
    patterns: []
processor:
    - name: "apdex_calculator/fixed"
      config:
        calculator: {type: "fixed", apdex_status: "satisfied"}
pipeline:
    - name: "traces_pipeline/common"
      type: "traces"
      processors:
        - "apdex_calculator/fixed"
`))
	require.NoError(t, err)
	require.Nil(t, mgr.GetPipeline(define.RecordMetrics))

	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{ctx: ctx, cancel: cancel, pipelineMgr: mgr, originalTasks: define.NewTaskQueue(define.PushModeGuarantee)}
	go c.consumeRecords()
	defer func() {
		cancel()
		c.wg.Wait()
	}()

	grpcBefore, httpBefore := noPipeline(t, define.RequestGrpc), noPipeline(t, define.RequestHttp)

	receiver.Publisher{}.Publish(&define.Record{RecordType: define.RecordTraces, RequestType: define.RequestHttp, Data: ptrace.NewTraces()})
	select {
	case task := <-c.originalTasks.Get():
		assert.Equal(t, define.RecordTraces, task.Record().RecordType)
	case <-time.After(5 * time.Second):
		t.Fatal("the traces record did not become a task")
	}

	receiver.Publisher{}.Publish(&define.Record{RecordType: define.RecordMetrics, RequestType: define.RequestGrpc, Data: pmetric.NewMetrics()})
	assert.Eventually(t, func() bool { return noPipeline(t, define.RequestGrpc) == grpcBefore+1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, httpBefore, noPipeline(t, define.RequestHttp))
	select {
	case task := <-c.originalTasks.Get():
		t.Fatalf("a record with no pipeline became a task: %v", task.Record().RecordType)
	default:
	}
}
