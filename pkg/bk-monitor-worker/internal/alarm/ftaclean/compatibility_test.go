// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ftaclean

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	tc "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/flat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/fta"
)

func TestTransferCompatibility(t *testing.T) {
	spec, msgs := fixtureSpec(t, 64)
	spec.ResultTableList = spec.ResultTableList[:1]
	// Only the first event matches. Other events have no matching alert name.
	spec.Option["clean_configs"] = []any{map[string]any{"rules": []map[string]any{{"key": "name", "method": "eq", "value": []string{"alert-0"}}}, "normalization_config": spec.Option["normalization_config"]}}
	delete(spec.Option, "normalization_config")
	p, err := compile(&spec, make(slots, 4))
	require.NoError(t, err)
	ctx := tc.ResultTableConfigIntoContext(tc.PipelineConfigIntoContext(context.Background(), &spec), spec.ResultTableList[0])
	splitter, err := flat.NewBatchProcessor(ctx, "test")
	require.NoError(t, err)
	alert, err := fta.NewAlertFTAProcessor(ctx, "test")
	require.NoError(t, err)
	flattened := make(chan define.Payload, 64)
	splitter.Process(define.NewJSONPayloadFrom(msgs[0].Value, 0), flattened, nil)
	close(flattened)
	legacy := make(chan define.Payload, 64)
	for event := range flattened {
		alert.Process(event, legacy, nil)
	}
	close(legacy)
	result, err := p.clean(ctx, msgs)
	require.NoError(t, err)
	require.Len(t, result[7], 1)
	require.Len(t, legacy, 1)
	var expected, actual map[string]any
	require.NoError(t, (<-legacy).To(&expected))
	v, _ := result[7][0].Value.Encode()
	require.NoError(t, json.Unmarshal(v, &actual))
	require.IsType(t, expected["bk_clean_time"], actual["bk_clean_time"])
	delete(actual, "bk_clean_time")
	delete(expected, "bk_clean_time")
	require.Equal(t, expected, actual)
}
