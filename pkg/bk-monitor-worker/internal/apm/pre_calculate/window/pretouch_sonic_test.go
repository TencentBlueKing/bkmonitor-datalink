// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License. See the License at http://opensource.org/licenses/MIT.

//go:build jsonsonic && ((amd64 && go1.17) || (arm64 && go1.20)) && !go1.26

package window

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

func TestSonicPretouchOriginMessageRoundTrip(t *testing.T) {
	require.Equal(t, sonic.UseSonicJSON, sonic.APIKind)
	// The production init precompiles this exact type; a fallback Pretouch would be a no-op.
	require.NoError(t, sonic.Pretouch(reflect.TypeOf(OriginMessage{})))
	data := []byte(`{"dataid":1573230,"datetime":"2026-10-10T00:00:00Z","items":[{
		"bk_biz_id":-111,"app_name":"监控应用","trace_id":"trace-a","span_id":"span-a",
		"parent_span_id":"","span_name":"<GET /api>&","kind":1,
		"start_time":1725000000001234,"end_time":1725000000004567,"elapsed_time":3333,
		"attributes":{"http.status_code":200,"http.url":"https://example.test/?a=1&b=2","empty":""},
		"status":{"code":0,"message":""},"resource":{"service.name":"api"}
	}]}`)
	var expected, actual OriginMessage
	require.NoError(t, json.Unmarshal(data, &expected))
	require.NoError(t, jsonx.Unmarshal(data, &actual))
	require.Equal(t, expected, actual)
	span := ToStandardSpan(actual.Items[0])
	require.Equal(t, "-111", span.BkBizId)
	require.Equal(t, "监控应用", span.AppName)
	require.Equal(t, 3333, span.EndTime-span.StartTime)
	encoded, err := jsonx.Marshal(actual)
	require.NoError(t, err)
	var roundTrip OriginMessage
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	require.Equal(t, expected, roundTrip)
}
