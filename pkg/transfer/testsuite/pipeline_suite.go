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
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/cstockton/go-conv"
	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// ETLSuite :
type ETLSuite struct {
	StoreSuite
	KillCh chan error
}

// SetupTest :
func (s *ETLSuite) SetupTest() {
	s.StoreSuite.SetupTest()
	s.KillCh = make(chan error)
}

// TearDownTest :
func (s *ETLSuite) TearDownTest() {
	s.StoreSuite.TearDownTest()
	close(s.KillCh)
}

// CheckKillChan :
func (s *ETLSuite) CheckKillChan(killCh <-chan error) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
	loop:
		for {
			select {
			case <-s.CTX.Done():
				break loop
			case err, ok := <-killCh:
				if !ok {
					break loop
				}
				switch errors.Cause(err) {
				case nil:
				case define.ErrTimeout:
				default:
					panic(err)
				}
			}
		}
		ch <- struct{}{}
	}()
	return ch
}

// MakePayload
func (s *ETLSuite) MakePayload(json string) *define.JSONPayload {
	return define.NewJSONPayloadFrom([]byte(json), 0)
}

// JSONEqual
func (s *ETLSuite) JSONEqual(excepted interface{}, result interface{}) {
	jExcepted, err := json.Marshal(excepted)
	s.NoError(err)

	jResult, err := json.Marshal(result)
	s.NoError(err)

	s.Equal(string(jExcepted), string(jResult))
}

// MapEqual
func (s *ETLSuite) MapEqual(excepted, result map[string]interface{}) {
	for key, value := range result {
		s.Equal(excepted[key], value, key)
		delete(result, key)
	}
	s.Len(result, 0)
}

// CheckPayloadEqual :
func (s *ETLSuite) CheckPayloadEqual(payload define.Payload, except map[string]interface{}) {
	results := make(map[string]interface{})
	s.NoError(payload.To(&results))
	for key, value := range except {
		result, ok := results[key]
		s.True(ok)
		s.Equal(value, result)
	}
}

// GetDimensions :
func (s *ETLSuite) GetDimensions(result map[string]interface{}) map[string]interface{} {
	value := result[define.RecordDimensionsFieldName]
	return value.(map[string]interface{})
}

// GetMetrics :
func (s *ETLSuite) GetMetrics(result map[string]interface{}) map[string]interface{} {
	value := result[define.RecordMetricsFieldName]
	return value.(map[string]interface{})
}

// GetTime :
func (s *ETLSuite) GetTime(result map[string]interface{}) int64 {
	return conv.Int64(result[define.TimeFieldName])
}

// GetGroup :
func (s *ETLSuite) GetGroup(result map[string]interface{}) []map[string]interface{} {
	res := result[define.RecordGroupFieldName].([]interface{})
	sliceGroup := make([]map[string]interface{}, len(res))
	for n, value := range res {
		sliceGroup[n] = value.(map[string]interface{})
	}
	return sliceGroup
}

// EqualRecord :
func (s *ETLSuite) EqualRecord(result, expects map[string]interface{}) {
	values := s.GetDimensions(result)
	expect := s.GetDimensions(expects)
	for key, value := range expect {
		s.Equalf(value, values[key], "dimension %s", key)
	}
	s.Equalf(len(expect), len(values), "dimensions")

	values = s.GetMetrics(result)
	expect = s.GetMetrics(expects)
	for key, value := range expect {
		s.Equalf(value, values[key], "metric %s", key)
	}
	s.Equalf(len(expect), len(values), "metrics")

	s.Equalf(s.GetTime(expects), s.GetTime(result), "time")
	// 有的清洗可能会没有group_info
	if expects[define.RecordGroupFieldName] != nil {
		exceptGroup := expects[define.RecordGroupFieldName].([]map[string]string)
		for n, v := range s.GetGroup(result) {
			for key, value := range v {
				s.Equalf(value, exceptGroup[n][conv.String(key)], "group")
			}
		}
	}
}

// Run
func (s *ETLSuite) Run(data string, processor define.DataProcessor, pushed func(result map[string]interface{})) {
	s.RunN(1, data, processor, pushed)
}

// RunN :
func (s *ETLSuite) RunN(count int, data string, processor define.DataProcessor, pushed func(result map[string]interface{})) {
	var wg sync.WaitGroup
	outputChan := make(chan define.Payload)
	KillChan := make(chan error)

	wg.Add(1)
	go func() {
		s.CheckKillChan(KillChan)
		wg.Done()
	}()

	payload := define.NewJSONPayloadFrom([]byte(data), 0)
	wg.Add(1)
	go func() {
		processor.Process(payload, outputChan, KillChan)
		close(outputChan)
		close(KillChan)
		wg.Done()
	}()

	for output := range outputChan {
		result := make(map[string]interface{})
		s.NoError(output.To(&result))
		pushed(result)
		count--
	}

	wg.Wait()
	s.Equal(0, count, data)
}

func (s *ETLSuite) EmptyHandler(record *define.ETLRecord) error {
	return nil
}

// ETLPipelineSuite :
type ETLPipelineSuite struct {
	ETLSuite
	NewFrontend    define.FrontendCreator
	NewBackend     define.BackendCreator
	ConsulConfig   string
	PipelineName   string
	FrontendPulled string
}

// SetupTest :
func (s *ETLPipelineSuite) SetupTest() {
	s.NoError(json.Unmarshal([]byte(s.ConsulConfig), &s.PipelineConfig))
	s.NewFrontend = define.NewFrontend
	s.NewBackend = define.NewBackend
	s.ETLSuite.SetupTest()
}

// TearDownTest :
func (s *ETLPipelineSuite) TearDownTest() {
	s.StoreSuite.TearDownTest()
	define.NewBackend = s.NewBackend
	define.NewFrontend = s.NewFrontend
}

// RunPipe :
func (s *ETLPipelineSuite) RunPipe(pipe define.Pipeline, fn func()) {
	killDone := s.CheckKillChan(pipe.Start())
	fn()
	s.NoError(pipe.Stop(0))
	s.NoError(pipe.Wait())
	<-killDone
}

// BuildPipe :
func (s *ETLPipelineSuite) BuildPipe(pulled func(define.Payload), pushed func(map[string]interface{})) define.Pipeline {
	s.NoError(s.PipelineConfig.Clean())
	frontend := NewMockFrontend(s.Ctrl)
	frontend.EXPECT().String().Return("frontend").AnyTimes()
	frontend.EXPECT().Close().Return(nil).AnyTimes()
	frontend.EXPECT().Pull(gomock.Any(), gomock.Any()).DoAndReturn(func(outputCh chan<- define.Payload, killCh chan<- error) {
		scanner := bufio.NewScanner(strings.NewReader(s.FrontendPulled))
		for scanner.Scan() {
			data := strings.TrimSpace(scanner.Text())
			if len(data) == 0 {
				continue
			}
			payload := define.NewJSONPayloadFrom([]byte(data), 0)
			pulled(payload)
			outputCh <- payload
			logging.Debugf("pulled %v\n", data)
		}
		killCh <- nil
		utils.TimeoutOrContextDone(s.CTX, time.After(time.Second))
	})
	define.NewFrontend = func(ctx context.Context, name string) (define.Frontend, error) {
		return frontend, nil
	}

	backend := NewMockBackend(s.Ctrl)
	backend.EXPECT().String().Return("backend").AnyTimes()
	backend.EXPECT().Close().Return(nil).AnyTimes()
	backend.EXPECT().Push(gomock.Any(), gomock.Any()).DoAndReturn(func(payload define.Payload, killCh chan<- error) {
		var data map[string]interface{}
		s.NoError(payload.To(&data))
		logging.Debugf("pushed %v\n", data)
		pushed(data)
	}).AnyTimes()
	define.NewBackend = func(ctx context.Context, name string) (define.Backend, error) {
		return backend, nil
	}

	pipe, err := define.NewPipeline(s.CTX, s.PipelineName)
	s.NoError(err)

	return pipe
}

// FreeSchemaETLPipelineSuite :
type FreeSchemaETLPipelineSuite struct {
	ETLPipelineSuite
	ConsulClient    *MockSourceClient
	NewConsulClient func(ctx context.Context) (consul.SourceClient, error)
}

// SetupTest :
func (s *FreeSchemaETLPipelineSuite) SetupTest() {
	s.ETLPipelineSuite.SetupTest()
	s.ConsulClient = NewMockSourceClient(s.Ctrl)
	s.NewConsulClient = consul.NewConsulClient
	consul.NewConsulClient = func(ctx context.Context) (client consul.SourceClient, e error) {
		return s.ConsulClient, nil
	}

	s.Config.SetDefault(consul.ConfKeySamplingInterval, time.Second)

	option := utils.NewMapHelper(s.ResultTableConfig.Option)
	option.SetDefault(config.ResultTableOptSchemaDiscovery, false)
	s.ResultTableConfig.Option = option.Data
	s.SetupReport()
}

// SetupReport
func (s *FreeSchemaETLPipelineSuite) SetupReport() {
	option := utils.NewMapHelper(s.ResultTableConfig.Option)
	if s.ResultTableConfig.SchemaType == config.ResultTableSchemaTypeFree && option.MustGetBool(config.ResultTableOptSchemaDiscovery) {
		s.ExceptReportData()
	} else {
		s.DisableReportData()
	}
}

// ExceptReportData :
func (s *FreeSchemaETLPipelineSuite) ExceptReportData() {
	option := utils.NewMapHelper(s.ResultTableConfig.Option)
	option.Set(config.ResultTableOptSchemaDiscovery, true)
	s.ConsulClient.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
}

// ExceptReportData :
func (s *FreeSchemaETLPipelineSuite) DisableReportData() {
	option := utils.NewMapHelper(s.ResultTableConfig.Option)
	option.Set(config.ResultTableOptSchemaDiscovery, false)
}

// TearDownTest :
func (s *FreeSchemaETLPipelineSuite) TearDownTest() {
	consul.NewConsulClient = s.NewConsulClient
	s.ETLPipelineSuite.TearDownTest()
}
