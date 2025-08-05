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
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
)

func TestConvert(t *testing.T) {
	buf := &bytes.Buffer{}
	content, err := os.ReadFile("../../example/fixtures/remotewrite.bytes")
	assert.NoError(t, err)
	buf.Write(content)

	wr, size, err := utils.DecodeWriteRequest(buf)
	assert.NoError(t, err)
	assert.Equal(t, 9979, size)

	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		for i := 0; i < len(evts); i++ {
			evt := evts[i]
			assert.Equal(t, define.RecordRemoteWrite, evt.RecordType())
			events = append(events, evt)
		}
	}

	NewCommonConverter(nil).Convert(&define.Record{
		RecordType:  define.RecordRemoteWrite,
		RequestType: define.RequestHttp,
		Data:        &define.RemoteWriteData{Timeseries: wr.Timeseries},
	}, gather)

	assert.Equal(t, 455, len(events))
	for i := 0; i < len(events); i++ {
		t.Logf("event(%d) = %+v", i, events[i].Data())
	}
}
