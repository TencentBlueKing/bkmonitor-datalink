// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/pipeline"
)

type benchFrontend struct {
	*define.BaseFrontend
	payload []byte
	n       int
}

// Pull
func (f *benchFrontend) Pull(outputChan chan<- define.Payload, killChan chan<- error) {
	for i := 0; i < f.n; i++ {
		payload := f.PayloadCreator()
		err := payload.From(f.payload)
		if err != nil {
			panic(err)
		}
		outputChan <- payload
	}
}

func newBenchFrontend(payload []byte, n int) *benchFrontend {
	return &benchFrontend{
		BaseFrontend: define.NewBaseFrontend(""),
		payload:      payload,
		n:            n,
	}
}

type benchBackend struct {
	*define.BaseBackend
	wg sync.WaitGroup
}

func (b *benchBackend) SetETLRecordFields(f *define.ETLRecordFields) {}

// Push
func (b *benchBackend) Push(d define.Payload, killChan chan<- error) {
	b.wg.Done()
}

// Wait
func (b *benchBackend) Wait() {
	b.wg.Wait()
}

func newBenchBackend(n int) *benchBackend {
	backend := &benchBackend{
		BaseBackend: define.NewBaseBackend(""),
	}
	backend.wg.Add(n)
	return backend
}

// BenchmarkUptimecheckTCPPipeline
func BenchmarkUptimecheckTCPPipeline(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defaultChannelBufferSize := pipeline.DefaultChannelBufferSize
	defer func() {
		pipeline.DefaultChannelBufferSize = defaultChannelBufferSize
	}()
	pipeline.DefaultChannelBufferSize = 10

	conf := config.NewConfiguration()
	ctx = config.IntoContext(ctx, conf)

	pipeConf := config.NewPipelineConfig()
	consulConfig := `{"etl_config":"bk_uptimecheck_tcp","result_table_list":[{"schema_type":"fixed","shipper_list":[{"cluster_type":"x"}],"result_table":"uptimecheck.tcp","field_list":[{"default_value":null,"type":"double","is_config_by_user":true,"tag":"metric","field_name":"available"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_biz_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_cloud_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_supplier_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"error_code"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"node_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"status"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"target_host"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"target_port"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"task_duration"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"task_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"task_type"},{"default_value":null,"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"time"}]}],"mq_config":{"cluster_type":"x"},"data_id":1009}`
	err := json.Unmarshal([]byte(consulConfig), &pipeConf)
	if err != nil {
		panic(err)
	}

	ctx = config.PipelineConfigIntoContext(ctx, pipeConf)

	rtConf := pipeConf.ResultTableList[0]
	ctx = config.ResultTableConfigIntoContext(ctx, rtConf)

	shipperConf := rtConf.ShipperList[0]
	ctx = config.ShipperConfigIntoContext(ctx, shipperConf)

	ctx = config.MQConfigIntoContext(ctx, pipeConf.MQConfig)

	store := storage.NewMapStore()
	ctx = define.StoreIntoContext(ctx, store)

	newFrontend := define.NewFrontend
	defer func() {
		define.NewFrontend = newFrontend
	}()

	hostInfo := models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
	}
	err = hostInfo.Dump(store, define.StoreNoExpires)
	if err != nil {
		panic(err)
	}

	payload := `{"available":1.000000,"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"zk-1","name":"zk-1","version":"1.3.2"},"bizid":0,"bk_biz_id":99,"bk_cloud_id":0,"cloudid":0,"dataid":1009,"error_code":0,"gseindex":66924,"ip":"127.0.0.1","node_id":6,"status":0,"target_host":"127.0.0.1","target_port":8301,"task_duration":0,"task_id":28,"task_type":"tcp","timestamp":1549528408,"type":"uptimecheckbeat"}`
	frontend := newBenchFrontend([]byte(payload), b.N)
	define.NewFrontend = func(ctx context.Context, name string) (define.Frontend, error) {
		return frontend, nil
	}

	newBackend := define.NewBackend
	defer func() {
		define.NewBackend = newBackend
	}()

	backend := newBenchBackend(b.N)
	define.NewBackend = func(ctx context.Context, name string) (define.Backend, error) {
		return backend, nil
	}

	pipe, err := template.NewUpTimeCheckTCPPipeline(ctx, "")
	if err != nil {
		panic(err)
	}

	b.ResetTimer()
	pipe.Start()
	backend.Wait()
	b.StopTimer()
	err = pipe.Stop(time.Second)
	if err != nil {
		panic(err)
	}
	err = pipe.Wait()
	if err != nil {
		panic(err)
	}
}
