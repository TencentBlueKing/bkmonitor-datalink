// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

func TestConvertFtaEvent(t *testing.T) {
	record := &define.Record{
		RecordType:  define.RecordFta,
		RequestType: define.RequestHttp,
		Token:       define.Token{Original: "xxx", MetricsDataId: 123},
		Data: &define.FtaData{
			PluginId:   "plugin_id",
			IngestTime: 123456789,
			Data:       []map[string]interface{}{{"key": "value"}},
			EventId:    "event_id",
		},
	}

	var conv ftaConverter
	conv.Convert(record, func(events ...define.Event) {
		assert.Len(t, events, 1)
		event := events[0].(FtaEvent)
		assert.Equal(t, define.Token{Original: "xxx", MetricsDataId: 123}, event.Token())
		assert.Equal(t, int32(123), event.DataId())
		assert.Equal(t, define.RecordFta, event.RecordType())
		assert.Equal(t, common.MapStr{
			"bk_data_id":      int32(123),
			"bk_plugin_id":    "plugin_id",
			"bk_ingest_time":  int64(123456789),
			"data":            []map[string]interface{}{{"key": "value"}},
			"__bk_event_id__": "event_id",
			"dataid":          int32(123),
		}, event.Data())
	})
}
