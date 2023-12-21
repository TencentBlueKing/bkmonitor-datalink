// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package script

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

var promutheusTestMeta = "sys_disk_size{mountpoint=\"/usr/local\"} 8.52597957704355"

// ScriptConfiSuite :
type ScriptFormatSuite struct {
	suite.Suite
}

// TestScriptConfig :
func TestScriptFormat(t *testing.T) {
	suite.Run(t, &ScriptFormatSuite{})
}

// TestFormat :
func (s *ScriptFormatSuite) TestFormat() {
	now := time.Now()
	handler, _ := tasks.GetTimestampHandler("s")
	fmResult, err := FormatOutput([]byte(promutheusTestMeta), now.UnixMilli(), 8760, handler)
	s.Equal(err, nil)
	s.Equal(len(fmResult), 1)
	for timestamp, eventData := range fmResult {
		s.Equal(now.Unix(), timestamp)
		s.Equal(len(eventData), 1)
		for _, pe := range eventData {
			s.Equal(len(pe.GetAggreValue()), 1)
			s.Equal(len(pe.GetLabels()), 1)
			s.Equal(pe.GetLabels()["mountpoint"], "/usr/local")
			s.Equal(pe.GetAggreValue()["sys_disk_size"], 8.52597957704355)
		}
	}
}

func (s *ScriptFormatSuite) TestNewProm() {
	testData := "http_requests_success{code=\"200\",method=\"post\"} 3 1595066363000"
	handler, err := tasks.GetTimestampHandler("s")
	s.Equal(err, nil)
	pe, err := tasks.NewPromEvent(testData, 123, 365*24*time.Hour*10, handler)
	s.Equal(err, nil)
	value := pe.GetAggreValue()
	s.Equal(len(value), 0)
	labels := pe.GetLabels()
	s.Equal(len(labels), 2)
	s.Equal(pe.GetTimestamp(), int64(1595066363))
}

func (s *ScriptFormatSuite) TestTimeStampProm() {
	testData := "http_requests_success{code=\"200\",method=\"post\"} 3 1395066363"
	handler, err := tasks.GetTimestampHandler("s")
	s.Equal(err, nil)
	now := time.Now()
	ts := now.UnixMilli()
	pe, err := tasks.NewPromEvent(testData, ts, 365*24*time.Hour, handler)
	s.Equal(err, nil)
	value := pe.GetAggreValue()
	s.Equal(len(value), 0)
	labels := pe.GetLabels()
	s.Equal(len(labels), 2)
	s.Equal(pe.GetTimestamp(), now.Unix())
}
