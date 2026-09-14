// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cstockton/go-conv"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// BulkHandlerSuite
type BulkHandlerSuite struct {
	testsuite.ETLSuite
	mockBulkWriter *testsuite.MockBulkWriter
	indexRender    elasticsearch.IndexRenderFn
	newBulkWriter  func(version string, config map[string]interface{}) (elasticsearch.BulkWriter, error)
}

// SetupTest
func (s *BulkHandlerSuite) SetupTest() {
	s.ETLSuite.SetupTest()
	s.newBulkWriter = elasticsearch.NewBulkWriter

	cluster := s.ShipperConfig.AsElasticSearchCluster()
	cluster.SetVersion("0.1")
	cluster.SetSchema("http")
	cluster.SetDomain("127.0.0.1")
	cluster.SetPort(9200)

	s.indexRender = elasticsearch.FixedIndexRender("test")

	s.mockBulkWriter = testsuite.NewMockBulkWriter(s.Ctrl)
	elasticsearch.NewBulkWriter = func(version string, config map[string]interface{}) (writer elasticsearch.BulkWriter, e error) {
		return s.mockBulkWriter, nil
	}
}

// TearDownTest
func (s *BulkHandlerSuite) TearDownTest() {
	s.ETLSuite.TearDownTest()
	elasticsearch.NewBulkWriter = s.newBulkWriter
}

// TestNew
func (s *BulkHandlerSuite) TestNew() {
	elasticsearch.NewBulkWriter = func(version string, config map[string]interface{}) (writer elasticsearch.BulkWriter, e error) {
		s.Equal(version, "v0")
		return s.mockBulkWriter, nil
	}
	cluster := s.ShipperConfig.AsElasticSearchCluster()
	handler, err := elasticsearch.NewBulkHandler(cluster, s.ResultTableConfig, time.Second, nil, s.indexRender)
	s.NoError(err)
	s.NotNil(handler)
}

func (s *BulkHandlerSuite) TestNewV8() {
	elasticsearch.NewBulkWriter = func(version string, config map[string]interface{}) (writer elasticsearch.BulkWriter, e error) {
		s.Equal("v8", version)
		return s.mockBulkWriter, nil
	}
	cluster := s.ShipperConfig.AsElasticSearchCluster()
	cluster.SetVersion("8.19.15")
	handler, err := elasticsearch.NewBulkHandler(cluster, s.ResultTableConfig, time.Second, nil, s.indexRender)
	s.NoError(err)
	s.NotNil(handler)
}

// TestFormatTime
func (s *BulkHandlerSuite) TestFormatTime() {
	s.ResultTableConfig.FieldList = append(
		s.ResultTableConfig.FieldList,
		&config.MetaFieldConfig{
			IsConfigByUser: true,
			FieldName:      "source_time",
			Type:           define.MetaFieldTypeTimestamp,
			Option: map[string]interface{}{
				config.MetaFieldOptESFormat: "timestamp",
			},
		},
		&config.MetaFieldConfig{
			IsConfigByUser: true,
			FieldName:      "time",
			Tag:            define.MetaFieldTagTime,
			Type:           define.MetaFieldTypeTimestamp,
			Option: map[string]interface{}{
				config.MetaFieldOptESFormat: "timestamp",
			},
		},
	)

	cluster := s.ShipperConfig.AsElasticSearchCluster()
	handler, err := elasticsearch.NewBulkHandler(cluster, s.ResultTableConfig, time.Second, nil, s.indexRender)
	s.NoError(err)

	now := time.Now()
	ts := now.Unix()

	s.mockBulkWriter.EXPECT().Write(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, index string, records elasticsearch.Records) (*elasticsearch.Response, error) {
		s.Len(records, 1)
		record := records[0].Document.(map[string]interface{})
		s.Equal(conv.String(ts), record["source_time"])
		s.Equal(conv.String(ts), record["time"])

		retIndex := map[string]interface{}{
			"_index":   "2_bkmonitor_event_1500279_20200331_0",
			"_type":    "_doc",
			"_id":      "0b049204f1cfcd70145a2a8c5175a789",
			"_version": 1,
			"result":   "created",
			"_shards": map[string]interface{}{
				"total":      2,
				"successful": 2,
				"failed":     0,
			},
			"_seq_no":       3054,
			"_primary_term": 1,
			"status":        201,
		}
		esResponse := map[string]interface{}{
			"took":   1,
			"errors": false,
			"items":  []map[string]interface{}{{"index": retIndex}},
		}
		data, err := json.Marshal(esResponse)
		s.NoError(err)
		return &elasticsearch.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer(data)),
		}, nil
	})

	record := define.ETLRecord{
		Time: &ts,
		Metrics: map[string]interface{}{
			"source_time": now,
		},
	}
	payload := define.NewJSONPayload(0)
	s.NoError(payload.From(&record))

	result, _, ok := handler.Handle(s.CTX, payload, s.KillCh)
	s.True(ok)

	cnt, err := handler.Flush(s.CTX, []interface{}{result})
	s.NoError(err)
	s.Equal(1, cnt)
}

func bulkResponse(statusCode int, body string) *elasticsearch.Response {
	return &elasticsearch.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func successfulBulkResponse(records elasticsearch.Records) *elasticsearch.Response {
	items := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		items = append(items, map[string]interface{}{
			"index": map[string]interface{}{
				"_id":    record.GetID(),
				"status": 201,
			},
		})
	}
	data, _ := json.Marshal(map[string]interface{}{
		"took":   1,
		"errors": false,
		"items":  items,
	})
	return bulkResponse(200, string(data))
}

func (s *BulkHandlerSuite) newHandler(indexRender elasticsearch.IndexRenderFn, uniqueFields []string) *elasticsearch.BulkHandler {
	cluster := s.ShipperConfig.AsElasticSearchCluster()
	handler, err := elasticsearch.NewBulkHandler(cluster, s.ResultTableConfig, time.Second, uniqueFields, indexRender)
	s.NoError(err)
	return handler
}

// TestFlushUsesLastIndex verifies records accumulated for the previous index are not sent to the new index.
func (s *BulkHandlerSuite) TestFlushUsesLastIndex() {
	handler := s.newHandler(func(record *elasticsearch.Record) (string, error) {
		return record.Document.(map[string]interface{})["target_index"].(string), nil
	}, nil)

	ts := int64(1710000000)
	records := []interface{}{
		&define.ETLRecord{Time: &ts, Metrics: map[string]interface{}{"target_index": "index-a"}},
		&define.ETLRecord{Time: &ts, Metrics: map[string]interface{}{"target_index": "index-b"}},
	}

	gomock.InOrder(
		s.mockBulkWriter.EXPECT().Write(gomock.Any(), "index-a", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, got elasticsearch.Records) (*elasticsearch.Response, error) {
				s.Len(got, 1)
				s.Equal("index-a", got[0].Document.(map[string]interface{})["target_index"])
				return successfulBulkResponse(got), nil
			},
		),
		s.mockBulkWriter.EXPECT().Write(gomock.Any(), "index-b", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, got elasticsearch.Records) (*elasticsearch.Response, error) {
				s.Len(got, 1)
				s.Equal("index-b", got[0].Document.(map[string]interface{})["target_index"])
				return successfulBulkResponse(got), nil
			},
		),
	)

	count, err := handler.Flush(s.CTX, records)
	s.NoError(err)
	s.Equal(2, count)
}

// TestFlushHTTPAndResponseErrors verifies every ambiguous or unsuccessful response is returned as an error.
func (s *BulkHandlerSuite) TestFlushHTTPAndResponseErrors() {
	handler := s.newHandler(s.indexRender, nil)
	ts := int64(1710000000)
	records := []interface{}{&define.ETLRecord{Time: &ts, Metrics: map[string]interface{}{"message": "test"}}}

	cases := []struct {
		name      string
		response  *elasticsearch.Response
		contains  string
		retryable bool
	}{
		{name: "http 400", response: bulkResponse(400, `{"error":"bad request"}`), contains: "400", retryable: false},
		{name: "http 429", response: bulkResponse(429, `{"error":"too many requests"}`), contains: "429", retryable: true},
		{name: "http 500", response: bulkResponse(500, `{"error":"server error"}`), contains: "500", retryable: true},
		{name: "nil response", response: nil, contains: "response is nil", retryable: true},
		{name: "nil body", response: &elasticsearch.Response{StatusCode: 200}, contains: "response body is nil", retryable: true},
		{name: "invalid json", response: bulkResponse(200, `{invalid`), contains: "parse elasticsearch response failed", retryable: true},
		{name: "item count mismatch", response: bulkResponse(200, `{"errors":false,"items":[]}`), contains: "item count mismatch", retryable: true},
	}

	for _, tc := range cases {
		s.Suite.Run(tc.name, func() {
			s.mockBulkWriter.EXPECT().Write(gomock.Any(), "test", gomock.Any()).Return(tc.response, nil)
			count, err := handler.Flush(s.CTX, records)
			s.Error(err)
			s.Contains(err.Error(), tc.contains)
			var retryable interface{ Retryable() bool }
			s.True(errors.As(err, &retryable))
			s.Equal(tc.retryable, retryable.Retryable())
			s.Zero(count)
		})
	}
}

func (s *BulkHandlerSuite) TestFlushTransportError() {
	handler := s.newHandler(s.indexRender, nil)
	ts := int64(1710000000)
	records := []interface{}{&define.ETLRecord{Time: &ts, Metrics: map[string]interface{}{"message": "test"}}}
	s.mockBulkWriter.EXPECT().Write(gomock.Any(), "test", gomock.Any()).Return(nil, errors.New("no response"))

	count, err := handler.Flush(s.CTX, records)
	s.Error(err)
	s.Contains(err.Error(), "write failed")
	var retryable interface{ Retryable() bool }
	s.True(errors.As(err, &retryable))
	s.True(retryable.Retryable())
	s.Zero(count)
}

// TestFlushPartialFailure returns the exact successes while still surfacing an error for retry.
func (s *BulkHandlerSuite) TestFlushPartialFailure() {
	handler := s.newHandler(s.indexRender, nil)
	ts := int64(1710000000)
	records := []interface{}{
		&define.ETLRecord{Time: &ts, Metrics: map[string]interface{}{"message": "ok"}},
		&define.ETLRecord{Time: &ts, Metrics: map[string]interface{}{"message": "bad"}},
	}
	body := `{
		"took": 1,
		"errors": true,
		"items": [
			{"index":{"_index":"test","_id":"ok","status":201}},
			{"index":{"_index":"test","_id":"bad","status":429,"error":{"type":"es_rejected_execution_exception","reason":"rejected"}}}
		]
	}`
	s.mockBulkWriter.EXPECT().Write(gomock.Any(), "test", gomock.Any()).Return(bulkResponse(200, body), nil)

	count, err := handler.Flush(s.CTX, records)
	s.Error(err)
	s.Contains(err.Error(), "1 succeeded, 1 failed")
	s.Contains(err.Error(), "status=429")
	s.Contains(err.Error(), "reason=rejected")
	var retryable interface{ Retryable() bool }
	s.True(errors.As(err, &retryable))
	s.True(retryable.Retryable())
	s.Equal(1, count)
}

func (s *BulkHandlerSuite) TestFlushPermanentPartialFailure() {
	handler := s.newHandler(s.indexRender, nil)
	ts := int64(1710000000)
	records := []interface{}{
		&define.ETLRecord{Time: &ts, Metrics: map[string]interface{}{"message": "ok"}},
		&define.ETLRecord{Time: &ts, Metrics: map[string]interface{}{"message": "bad"}},
	}
	body := `{
		"took": 1,
		"errors": true,
		"items": [
			{"index":{"_index":"test","_id":"ok","status":201}},
			{"index":{"_index":"test","_id":"bad","status":400,"error":{"type":"mapper_parsing_exception","reason":"bad field"}}}
		]
	}`
	s.mockBulkWriter.EXPECT().Write(gomock.Any(), "test", gomock.Any()).Return(bulkResponse(200, body), nil)

	count, err := handler.Flush(s.CTX, records)
	s.Error(err)
	var retryable interface{ Retryable() bool }
	s.True(errors.As(err, &retryable))
	s.False(retryable.Retryable())
	s.Equal(1, count)
}

// TestLogsDBTimestampIsShipperLocal verifies timestamp injection neither changes the deterministic ID nor shared input.
func (s *BulkHandlerSuite) TestLogsDBTimestampIsShipperLocal() {
	cluster := s.ShipperConfig.AsElasticSearchCluster()
	cluster.StorageConfig["index_settings"] = map[string]interface{}{"index.mode": "logsdb"}
	logsDBHandler := s.newHandler(s.indexRender, []string{"message"})
	cluster.StorageConfig["index_settings"] = map[string]interface{}{}
	standardHandler := s.newHandler(s.indexRender, []string{"message"})

	ts := int64(1710000000)
	etlRecord := &define.ETLRecord{
		Time: &ts,
		Metrics: map[string]interface{}{
			"message":          "same-event",
			"@timestamp":       "business-value",
			"dtEventTimeStamp": int64(1710000000),
		},
	}
	var logsDBID, standardID string
	gomock.InOrder(
		s.mockBulkWriter.EXPECT().Write(gomock.Any(), "test", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, got elasticsearch.Records) (*elasticsearch.Response, error) {
				document := got[0].Document.(map[string]interface{})
				s.Equal(int64(1710000000000), document["@timestamp"])
				s.Contains(document, "time")
				s.Equal(int64(1710000000), document["dtEventTimeStamp"])
				logsDBID = got[0].GetID()
				return successfulBulkResponse(got), nil
			},
		),
		s.mockBulkWriter.EXPECT().Write(gomock.Any(), "test", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, got elasticsearch.Records) (*elasticsearch.Response, error) {
				document := got[0].Document.(map[string]interface{})
				s.Equal("business-value", document["@timestamp"])
				standardID = got[0].GetID()
				return successfulBulkResponse(got), nil
			},
		),
	)

	count, err := logsDBHandler.Flush(s.CTX, []interface{}{etlRecord})
	s.NoError(err)
	s.Equal(1, count)
	s.Equal("business-value", etlRecord.Metrics["@timestamp"])

	count, err = standardHandler.Flush(s.CTX, []interface{}{etlRecord})
	s.NoError(err)
	s.Equal(1, count)
	s.Equal(logsDBID, standardID)
}

func (s *BulkHandlerSuite) TestStandardShipperDoesNotInjectTimestamp() {
	handler := s.newHandler(s.indexRender, nil)
	ts := int64(1710000000)
	etlRecord := &define.ETLRecord{Time: &ts, Metrics: map[string]interface{}{"message": "standard"}}
	s.mockBulkWriter.EXPECT().Write(gomock.Any(), "test", gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, got elasticsearch.Records) (*elasticsearch.Response, error) {
			s.NotContains(got[0].Document.(map[string]interface{}), "@timestamp")
			return successfulBulkResponse(got), nil
		},
	)

	count, err := handler.Flush(s.CTX, []interface{}{etlRecord})
	s.NoError(err)
	s.Equal(1, count)
}

// TestMissingRecordTimeIsExplicitError verifies a malformed record cannot panic or use the current time.
func (s *BulkHandlerSuite) TestMissingRecordTimeIsExplicitError() {
	handler := s.newHandler(s.indexRender, nil)
	etlRecord := &define.ETLRecord{Metrics: map[string]interface{}{"message": "missing-time"}}
	payload := define.NewJSONPayload(0)
	s.NoError(payload.From(etlRecord))

	result, at, ok := handler.Handle(s.CTX, payload, s.KillCh)
	s.True(ok)
	s.True(at.IsZero())

	count, err := handler.Flush(s.CTX, []interface{}{result})
	s.Error(err)
	s.Contains(err.Error(), "record time is empty")
	var retryable interface{ Retryable() bool }
	s.True(errors.As(err, &retryable))
	s.False(retryable.Retryable())
	s.Zero(count)
}

// TestBulkHandlerSuite
func TestBulkHandlerSuite(t *testing.T) {
	suite.Run(t, new(BulkHandlerSuite))
}
