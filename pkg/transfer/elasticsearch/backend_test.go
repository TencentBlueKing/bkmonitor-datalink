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
	"io"
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
			"items":  []map[string]interface{}{retIndex},
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

// TestBulkHandlerSuite
func TestBulkHandlerSuite(t *testing.T) {
	suite.Run(t, new(BulkHandlerSuite))
}
