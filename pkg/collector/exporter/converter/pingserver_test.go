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
)

func TestConvertPingserverData(t *testing.T) {
	pd := &define.PingserverData{
		DataId:  1001,
		Version: "1.0",
		Data:    map[string]any{"data": "data"},
	}

	events := make([]define.Event, 0)
	var conv pingserverConverter
	defer conv.Clean()

	conv.Convert(&define.Record{
		RecordType: define.RecordPingserver,
		Data:       pd,
	}, func(evts ...define.Event) {
		for _, evt := range evts {
			assert.Equal(t, define.RecordPingserver, evt.RecordType())
			assert.Equal(t, int32(1001), evt.DataId())
			events = append(events, evt)
		}
	})

	assert.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, event.DataId(), int32(1001))

	data := event.Data()
	assert.Equal(t, data["dataid"], int64(1001))
	assert.Equal(t, data["data"], []map[string]any{{"data": "data"}})
	assert.Equal(t, data["version"], "1.0")
}
