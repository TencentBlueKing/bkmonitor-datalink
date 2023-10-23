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
	"runtime/pprof"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func makeFakeProfilesData(t string) []byte {
	var buf bytes.Buffer
	pprof.Lookup(t).WriteTo(&buf, 1)
	return buf.Bytes()
}

func TestConvertProfilesData(t *testing.T) {
	pd := makeFakeProfilesData("goroutine")

	events := make([]define.Event, 0)
	NewCommonConverter().Convert(&define.Record{
		RecordType: define.RecordProfiles,
		Data:       pd,
		Token: define.Token{
			AppName: "testa",
			BizId:   1,
		},
	}, func(evts ...define.Event) {
		events = append(events, evts...)
	})

	assert.Len(t, events, 1)

	event := events[0]
	data := event.Data()
	assert.Equal(t, data["biz_id"], int32(1))
	assert.Equal(t, data["app"], "testa")
	assert.Equal(t, data["type"], "goroutine")
}
