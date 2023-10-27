// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package formatter_test

import (
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/formatter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// ProcessorSuite :
type ProcessorSuite struct {
	testsuite.ConfigSuite
}

// TestProcess :
func (s *ProcessorSuite) TestProcess() {
	payload := testsuite.NewMockPayload(s.Ctrl)
	payload.EXPECT().To(gomock.Any()).Return(nil)
	payload.EXPECT().From(gomock.Any()).Return(nil)
	payload.EXPECT().Type().Return("")
	payload.EXPECT().SN().Return(0)
	payload.EXPECT().GetTime().AnyTimes()
	payload.EXPECT().SetTime(gomock.Any()).AnyTimes()
	newPayload := define.NewPayload
	defer func() {
		define.NewPayload = newPayload
	}()

	define.NewPayload = func(name string, sn int) (define.Payload, error) {
		return payload, nil
	}

	trace := make([]string, 0, 3)

	processor := NewProcessor(s.CTX, "test", RecordHandlers{
		func(record *define.ETLRecord, handler define.ETLRecordHandler) error {
			trace = append(trace, "1")
			return handler(record)
		},
		func(record *define.ETLRecord, handler define.ETLRecordHandler) error {
			err := handler(record)
			trace = append(trace, "3")
			return err
		},
		func(record *define.ETLRecord, handler define.ETLRecordHandler) error {
			trace = append(trace, "2")
			return handler(record)
		},
	})

	var wg sync.WaitGroup
	outputCh := make(chan define.Payload)
	killCh := make(chan error)

	wg.Add(1)
	go func() {
		for err := range killCh {
			panic(err)
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		processor.Process(payload, outputCh, killCh)
		close(outputCh)
		close(killCh)
		wg.Done()
	}()

	for range outputCh {
	}

	wg.Wait()
	s.Equal("1", trace[0])
	s.Equal("2", trace[1])
	s.Equal("3", trace[2])
}

// TestProcessorSuite :
func TestProcessorSuite(t *testing.T) {
	suite.Run(t, new(ProcessorSuite))
}
