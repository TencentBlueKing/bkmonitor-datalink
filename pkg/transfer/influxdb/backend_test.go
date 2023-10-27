// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb_test

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cstockton/go-conv"
	"github.com/golang/mock/gomock"
	"github.com/influxdata/influxdb/client/v2"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// InfluxDBSuite
type InfluxDBSuite struct {
	ETLSuite
	client        *MockInfluxDBClient
	newHTTPClient func(influxdb.HTTPConfig) (influxdb.Client, error)
}

// SetupTest :
func (s *InfluxDBSuite) SetupTest() {
	s.ETLSuite.SetupTest()
	influxCluster := s.ShipperConfig.AsInfluxCluster()
	influxCluster.SetSchema("http")
	influxCluster.SetDomain("localhost")
	influxCluster.SetPort(88888888)
	influxCluster.SetDataBase("database")
	influxCluster.SetTable("table")
	influxCluster.SetRetentionPolicy("default")

	config.NewAuthInfo(s.ShipperConfig).SetUserName("")
	config.NewAuthInfo(s.ShipperConfig).SetPassword("")

	cli := NewMockInfluxDBClient(s.Ctrl)
	cli.EXPECT().Close().AnyTimes()

	s.client = cli
	s.newHTTPClient = influxdb.NewHTTPClient
	influxdb.NewHTTPClient = func(influxdb.HTTPConfig) (influxdb.Client, error) {
		return cli, nil
	}
}

// TearDownTest :
func (s *InfluxDBSuite) TearDownTest() {
	s.ETLSuite.TearDownTest()
	influxdb.NewHTTPClient = s.newHTTPClient
}

// BackendSuite :
type BackendSuite struct {
	InfluxDBSuite
}

// TestPushFull : 测试提交的数据格式
func (s *BackendSuite) TestPushData() {
	cases := []struct {
		tag, field, drop bool
		data             string
	}{
		{
			false, true, false,
			`{"time":%d,"dimensions":{"tag":null},"metrics":{"field":1}}`,
		},
		{
			false, true, false,
			`{"time":%d,"dimensions":{"tag":""},"metrics":{"field":1}}`,
		},
		{
			true, true, false,
			`{"time":%d,"dimensions":{"tag":"0"},"metrics":{"field":1}}`,
		},
		{
			true, true, false,
			`{"time":%d,"dimensions":{"tag":"0"},"metrics":{"field":0}}`,
		},
		{
			true, false, true,
			`{"time":%d,"dimensions":{"tag":"0"},"metrics":{"field":null}}`,
		},
		{
			true, true, false,
			`{"time":%d,"dimensions":{"tag":"0"},"metrics":{"field":""}}`,
		},
		{
			true, false, true,
			`{"time":%d,"dimensions":{"tag":"0"},"metrics":{}}`,
		},
		{
			true, false, true,
			`{"time":%d,"dimensions":{"tag":"0"}}`,
		},
	}

	ch := make(chan int)
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		for _, p := range bp.Points() {
			index := p.Time().Unix() / 60
			c := cases[index]
			s.False(c.drop)
			tags := p.Tags()
			if c.tag {
				s.True(len(tags) > 0)
			} else {
				s.True(len(tags) == 0)
			}

			fields, err := p.Fields()
			s.NoError(err)
			if c.field {
				s.True(len(fields) > 0)
			} else {
				s.True(len(fields) == 0)
			}
		}
		ch <- len(bp.Points())
		return nil
	}).AnyTimes()

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 1)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Millisecond)

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)

	s.CheckKillChan(s.KillCh)

	for i, c := range cases {
		payload := define.NewJSONPayloadFrom([]byte(fmt.Sprintf(c.data, i)), 0)
		backend.Push(payload, s.KillCh)
		if !c.drop {
			s.Equal(1, <-ch)
		}
	}
	s.NoError(backend.Close())
}

// TestPushFull : 测试因缓冲区满而触发的提交
func (s *BackendSuite) TestPushFull() {
	cases := []string{
		`{"time":1547616480,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616481,"dimensions":{"index":"1"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616482,"dimensions":{"index":"2"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616493,"dimensions":{"index":"3"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
	}

	var wg sync.WaitGroup
	size := 2
	wg.Add(len(cases))
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		s.Equal(size, len(bp.Points()))
		for range bp.Points() {
			wg.Done()
		}
		return nil
	}).Times(len(cases) / 2)

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, size)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Hour)

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)

	s.CheckKillChan(s.KillCh)

	for _, c := range cases {
		payload := define.NewJSONPayloadFrom([]byte(c), 0)
		backend.Push(payload, s.KillCh)
	}

	wg.Wait()
	s.NoError(backend.Close())
}

// TestPushInterval : 测试因超时而触发的提交
func (s *BackendSuite) TestPushInterval() {
	cases := []string{
		`{"time":1547616490,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616491,"dimensions":{"index":"1"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616492,"dimensions":{"index":"2"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616493,"dimensions":{"index":"3"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
	}

	var wg sync.WaitGroup
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		points := bp.Points()
		s.Equal(1, len(points))
		wg.Done()
		return nil
	}).Times(len(cases))

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 2*len(cases))
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Millisecond)

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)

	s.CheckKillChan(s.KillCh)

	for _, c := range cases {
		payload := define.NewJSONPayloadFrom([]byte(c), 0)
		wg.Add(1)
		backend.Push(payload, s.KillCh)
		wg.Wait()
	}

	s.NoError(backend.Close())
}

// TestPushRemains : 测试因关闭而触发的提交
func (s *BackendSuite) TestPushRemains() {
	cases := []string{
		`{"time":1547616400,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616401,"dimensions":{"index":"1"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616402,"dimensions":{"index":"2"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616403,"dimensions":{"index":"3"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
	}

	var wg sync.WaitGroup
	wg.Add(len(cases))
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		for _, data := range bp.Points() {
			fmt.Printf("%#v", data)
			wg.Done()
		}
		return nil
	})

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 2*len(cases))
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Hour)

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)

	s.CheckKillChan(s.KillCh)

	for _, c := range cases {
		payload := define.NewJSONPayloadFrom([]byte(c), 0)
		backend.Push(payload, s.KillCh)
	}

	s.NoError(backend.Close())
	wg.Wait()
}

// TestPushRetries : 测试提交失败导致的重试
func (s *BackendSuite) TestPushRetries() {
	cases := []string{
		`{"time":1547616410,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
	}

	written := 2
	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 1)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Millisecond)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushRetries, written)

	var wg sync.WaitGroup
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		written--
		if written > 0 {
			return fmt.Errorf("test")
		}
		s.Equal(1, len(bp.Points()))
		wg.Done()
		return nil
	}).AnyTimes()

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)

	s.CheckKillChan(s.KillCh)

	for _, c := range cases {
		payload := define.NewJSONPayloadFrom([]byte(c), 0)
		wg.Add(1)
		backend.Push(payload, s.KillCh)
	}

	s.NoError(backend.Close())
	wg.Wait()
}

func (s *BackendSuite) TestExemplarSplitMeasurement() {
	cases := []string{
		`{"time":1547616420,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true},"exemplar": {"bk_span_id": "span", "bk_trace_id": "trace", "bk_trace_timestamp": 1.0, "bk_trace_value": 100 }}`,
	}

	// 配置流水线backend
	c := config.ResultTableConfigFromContext(s.CTX)
	c.Option["is_split_measurement"] = true

	ch := make(chan []*client.Point, 10)
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		fmt.Printf("ready to push")
		ch <- bp.Points()
		return nil
	}).AnyTimes()

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 1)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Millisecond)

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)
	s.CheckKillChan(s.KillCh)

	var wg sync.WaitGroup

	for _, c := range cases {
		payload := define.NewJSONPayloadFrom([]byte(c), 0)
		backend.Push(payload, s.KillCh)
		result := <-ch

		wg.Add(4)
		go func() {
			for _, row := range result {
				wg.Done()
				fields, _ := row.Fields()
				s.Equal(fields["bk_span_id"], "span")
			}
		}()
	}

	wg.Wait()
	s.NoError(backend.Close())
	// 将分表配置关闭，避免影响其他测试
	c.Option["is_split_measurement"] = false
}

func (s *BackendSuite) TestMustIncludeDimensions() {
	cases := []string{
		`{"time":1547616420,"dimensions":{"index":"0","foo":"bar"},"metrics":{"load1":1}}`,
		`{"time":1547616420,"dimensions":{"index":"0","foo":"bar"},"metrics":{"load5":5}}`,
		`{"time":1547616420,"dimensions":{"index":"0","foo":"bar", "foz":"bar"},"metrics":{"usage":5}}`,
		`{"time":1547616420,"dimensions":{"index":"0"},"metrics":{"load15":15}}`, // drop
		`{"time":1547616420,"dimensions":{},"metrics":{"dropped":15}}`,           // drop
	}

	// 配置流水线backend
	c := config.ResultTableConfigFromContext(s.CTX)
	c.Option["must_include_dimensions"] = []string{"index", "foo"}

	ch := make(chan []*client.Point, 10)
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		ch <- bp.Points()
		return nil
	}).AnyTimes()

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 1)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Millisecond)

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)
	s.CheckKillChan(s.KillCh)

	for _, c := range cases {
		payload := define.NewJSONPayloadFrom([]byte(c), 0)
		backend.Push(payload, s.KillCh)
	}

	var points []*client.Point
	for i := 0; i < 3; i++ {
		points = append(points, <-ch...)
	}

	excepted := map[string]struct{}{
		"table,foo=bar,index=0 load1=1 1547616420000000000":         {},
		"table,foo=bar,foz=bar,index=0 usage=5 1547616420000000000": {},
		"table,foo=bar,index=0 load5=5 1547616420000000000":         {},
	}

	for _, point := range points {
		_, ok := excepted[point.String()]
		s.True(ok)
	}
	s.NoError(backend.Close())
}

func (s *BackendSuite) TestExemplar() {
	cases := []string{
		`{"time":1547616420,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true},"exemplar": {"bk_span_id": "span", "bk_trace_id": "trace", "bk_trace_timestamp": 1.0, "bk_trace_value": 100 }}`,
	}

	// 配置流水线backend
	ch := make(chan []*client.Point, 10)
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		fmt.Printf("ready to push")
		ch <- bp.Points()
		return nil
	}).AnyTimes()

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 1)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Millisecond)

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)
	s.CheckKillChan(s.KillCh)

	for _, c := range cases {
		payload := define.NewJSONPayloadFrom([]byte(c), 0)
		backend.Push(payload, s.KillCh)
		result := <-ch

		for _, row := range result {
			fields, _ := row.Fields()
			s.Equal(fields["bk_span_id"], "span")
		}
	}

	s.NoError(backend.Close())
}

// TestPushDrop : 测试重试次数过多丢弃数据
func (s *BackendSuite) TestPushDrop() {
	cases := []string{
		`{"time":1547616420,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
		`{"time":1547616421,"dimensions":{"index":"1"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`,
	}

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 1)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Millisecond)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushRetries, 1)

	var wg sync.WaitGroup
	var written int64
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		atomic.AddInt64(&written, 1)
		return fmt.Errorf("test")
	}).Times(2 * len(cases))

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)

	s.CheckKillChan(s.KillCh)

	for _, c := range cases {
		payload := define.NewJSONPayloadFrom([]byte(c), 0)
		backend.Push(payload, s.KillCh)
	}

	wg.Wait()
	s.NoError(backend.Close())
	s.Equal(int64(2*len(cases)), written)
}

func (s *BackendSuite) TestPushRateLimiter10Qps() {
	data := `{"time":1547616420,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 1)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Millisecond*100)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushRetries, 1)

	const times = 60
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		return nil
	}).Times(times)

	backend, err := influxdb.NewBackend(s.CTX, "test", 10)
	s.NoError(err)
	s.CheckKillChan(s.KillCh)

	start := time.Now()
	for n := 0; n < times; n++ {
		payload := define.NewJSONPayloadFrom([]byte(data), 0)
		backend.Push(payload, s.KillCh)
	}
	seconds := time.Since(start).Seconds()
	s.True(seconds > 3)

	s.NoError(backend.Close())
}

func (s *BackendSuite) TestPushRateLimiterInfinite() {
	data := `{"time":1547616420,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true}}`

	s.Stubs.Stub(&pipeline.BulkDefaultBufferSize, 1)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushInterval, time.Millisecond*100)
	s.Stubs.Stub(&pipeline.BulkDefaultFlushRetries, 1)

	const times = 200
	s.client.EXPECT().Write(gomock.Any()).DoAndReturn(func(bp client.BatchPoints) error {
		return nil
	}).Times(times)

	backend, err := influxdb.NewBackend(s.CTX, "test", 0)
	s.NoError(err)
	s.CheckKillChan(s.KillCh)

	start := time.Now()
	for n := 0; n < times; n++ {
		payload := define.NewJSONPayloadFrom([]byte(data), 0)
		backend.Push(payload, s.KillCh)
	}
	seconds := time.Since(start).Seconds()
	s.True(seconds < 1)

	s.NoError(backend.Close())
}

// TestBackend :
func TestBackend(t *testing.T) {
	suite.Run(t, new(BackendSuite))
}

// BulkHandlerSuite
type BulkHandlerSuite struct {
	InfluxDBSuite
}

// TestDisabledField
func (s *BulkHandlerSuite) TestDisabledField() {
	cases := []struct {
		tag        define.MetaFieldTagType
		disabled   bool
		dimensions string
		metrics    string
		dropped    bool
	}{
		{define.MetaFieldTagDimension, false, "x", "", true},
		{define.MetaFieldTagMetric, false, "a", "b,x", false},
		{define.MetaFieldTagDimension, false, "a,x", "b", false},
		{define.MetaFieldTagDimension, false, "a,x", "b", false},
		{define.MetaFieldTagDimension, true, "a,x", "b", false},
		{define.MetaFieldTagDimension, true, "x", "b", false},
		{define.MetaFieldTagMetric, false, "a", "b,x", false},
		{define.MetaFieldTagMetric, true, "a", "b,x", false},
		{define.MetaFieldTagMetric, true, "a", "x", true},
	}

	killCh := make(chan error)
	s.CheckKillChan(killCh)

	for i, c := range cases {
		field := &config.MetaFieldConfig{
			IsConfigByUser: true,
			FieldName:      "x",
			Tag:            c.tag,
			Option: map[string]interface{}{
				config.MetaFieldOptInfluxDisabled: c.disabled,
			},
		}
		s.ResultTableConfig.FieldList = []*config.MetaFieldConfig{field}

		record := define.NewETLRecord()
		for index, name := range strings.Split(c.dimensions, ",") {
			if name == "" {
				continue
			}
			record.Dimensions[name] = conv.String(index)
			if name != "x" {
				s.ResultTableConfig.FieldList = append(s.ResultTableConfig.FieldList, &config.MetaFieldConfig{
					IsConfigByUser: true,
					FieldName:      name,
					Tag:            define.MetaFieldTagDimension,
				})
			}
		}
		for index, name := range strings.Split(c.metrics, ",") {
			if name == "" {
				continue
			}
			record.Metrics[name] = index
			if name != "x" {
				s.ResultTableConfig.FieldList = append(s.ResultTableConfig.FieldList, &config.MetaFieldConfig{
					FieldName: name,
					Tag:       define.MetaFieldTagMetric,
				})
			}
		}

		payload := define.NewJSONPayload(0)
		s.NoError(payload.From(record))

		bulk, err := influxdb.NewBulkHandler(s.ResultTableConfig, s.ShipperConfig)
		s.NoError(err, i)
		result, _, ok := bulk.Handle(s.CTX, payload, killCh)

		if c.dropped {
			s.False(ok, i)
			s.Nil(result, i)
			continue
		}

		point := result.(*client.Point)
		if c.tag == define.MetaFieldTagDimension {
			_, ok := record.Dimensions["x"]
			s.True(ok, i)
			_, ok = point.Tags()["x"]
			s.Equal(!c.disabled, ok, i)
		} else if c.tag == define.MetaFieldTagMetric {
			_, ok := record.Metrics["x"]
			s.True(ok, i)
			fields, err := point.Fields()
			s.NoError(err, i)
			_, ok = fields["x"]
			s.Equal(!c.disabled, ok, i)
		}
	}
	close(killCh)
}

// TestBulkHandlerSuite
func TestBulkHandlerSuite(t *testing.T) {
	suite.Run(t, new(BulkHandlerSuite))
}
