// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package testsuite

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

type newProcessor func(ctx context.Context, name string) define.DataProcessor

// NewTestProcessorData :
// todo 在其他opt分支合并后,删除该代码
func NewTestProcessorData(ctx context.Context, name string, data []byte, fn newProcessor, testData []byte) (define.DataProcessor, map[string]interface{}, define.Payload, error) {
	/*
		ctx,name
		data:测试数据
		testData: 测试数据期望输出
		fn : NewProcessor
	*/
	var resultTable map[string]interface{}
	err := json.Unmarshal(testData, &resultTable)
	payload := define.NewJSONPayloadFrom(data, 0)
	return fn(ctx, name), resultTable, payload, err
}

// GetCtxFromConsulConfig : 根据consul生成ctx
func PipelineConfigStringInfoContext(ctx context.Context, pipelineConfig *config.PipelineConfig, consul string) context.Context {
	err := json.Unmarshal([]byte(consul), &pipelineConfig)
	if err != nil {
		panic(err)
	}
	return config.PipelineConfigIntoContext(ctx, pipelineConfig)
}

// EtlEqualDeviceName
func EtlEqualDeviceName(deviceName string, result map[string]interface{}) string {
	if deviceValue, ok := result["dimensions"].(map[string]interface{})[deviceName].(string); ok {
		return deviceValue
	}
	panic(fmt.Errorf("convert fail, no device_name found in %v", result))
}

// ETLBenchmarkTest : etl benchmark
func ETLBenchmarkTest(b *testing.B, newProcess newProcessor, data []byte) {
	var (
		outputChan     chan define.Payload
		KillChan       chan error
		pipelineConfig *config.PipelineConfig
		name           = "test"
		CTX            = context.Background()
	)

	ctx := config.IntoContext(CTX, config.NewConfiguration())
	ctx = config.ResultTableConfigIntoContext(ctx, &config.MetaResultTableConfig{})
	pipelineConfig = config.NewPipelineConfig()
	consulConfig := `{"etl_config":"bk_uptimecheck_tcp","result_table_list":[{"schema_type":"fixed","shipper_list":[{"cluster_config":{"domain_name":"influxdb.service.consul","port":5260},"storage_config":{"real_table_name":"tcp","database":"uptimecheck"},"cluster_type":"influxdb"}],"result_table":"uptimecheck.tcp","field_list":[{"default_value":null,"type":"double","is_config_by_user":true,"tag":"metric","field_name":"available"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_biz_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_cloud_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_supplier_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"error_code"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"node_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"status"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"target_host"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"target_port"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"task_duration"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"task_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"task_type"},{"default_value":null,"type":"timestamp","is_config_by_user":true,"tag":"timestamp","field_name":"timestamp"}]}],"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_10090","partition":1},"cluster_type":"kafka"},"data_id":1009}`
	_ = json.Unmarshal([]byte(consulConfig), &pipelineConfig)
	ctx = config.PipelineConfigIntoContext(ctx, pipelineConfig)

	p := newProcess(ctx, name)
	payload := define.NewJSONPayloadFrom(data, 0)

	b.StopTimer()
	for i := 0; i < b.N; i++ {
		outputChan = make(chan define.Payload)
		KillChan = make(chan error)
		go func() {
			defer close(outputChan)
			defer close(KillChan)
			b.StartTimer()
			p.Process(payload, outputChan, KillChan)
			b.StopTimer()
		}()
		for range outputChan {
		}
	}
}
