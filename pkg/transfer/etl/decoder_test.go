// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
)

// PayloadDecoderSuite
type PayloadDecoderSuite struct {
	suite.Suite
	decoder *etl.PayloadDecoder
}

// SetupTest
func (s *PayloadDecoderSuite) SetupTest() {
	s.decoder = etl.NewPayloadDecoder()
}

// TestDecode
func (s *PayloadDecoderSuite) TestDecode() {
	payload := define.NewJSONPayloadFrom([]byte(`{"name":"test"}`), 0)
	containers, err := s.decoder.Decode(payload)
	s.NoError(err)
	s.Len(containers, 1)
	name, err := containers[0].Get("name")
	s.NoError(err)
	s.Equal("test", name)
}

// TestFissionSplitHandler
func (s *PayloadDecoderSuite) TestFissionSplitHandler() {
	payload := define.NewJSONPayloadFrom([]byte(`{"name":"test","values":[1,2]}`), 0)
	s.decoder.FissionSplitHandler(true, etl.ExtractByJMESPath("values"), "$index", "$value")
	containers, err := s.decoder.Decode(payload)
	s.NoError(err)
	s.Len(containers, 2)

	for i, container := range containers {
		value, err := container.Get("$value")
		s.NoError(err)
		s.Equal(float64(i+1), value)

		index, err := container.Get("$index")
		s.NoError(err)
		s.Equal(i, index)
	}
}

// TestFissionMergeHandler
func (s *PayloadDecoderSuite) TestFissionMergeHandler() {
	payload := define.NewJSONPayloadFrom([]byte(`{"name":"test","values":[{"value":1},{"value":2}]}`), 0)
	s.decoder.FissionMergeHandler(true, etl.ExtractByJMESPath("values"), "$index")
	containers, err := s.decoder.Decode(payload)
	s.NoError(err)
	s.Len(containers, 2)

	for i, container := range containers {
		value, err := container.Get("value")
		s.NoError(err)
		s.Equal(float64(i+1), value)

		index, err := container.Get("$index")
		s.NoError(err)
		s.Equal(i, index)
	}
}

// TestFissionMergeIntoHandler
func (s *PayloadDecoderSuite) TestFissionMergeIntoHandler() {
	payload := define.NewJSONPayloadFrom([]byte(`{"name":"test","values":[{"value":1},{"value":2}]}`), 0)
	s.decoder.FissionMergeIntoHandler(true, etl.ExtractByJMESPath("values"), "attribute")
	containers, err := s.decoder.Decode(payload)
	s.NoError(err)
	s.Len(containers, 2)
	for i, container := range containers {
		attribute, err := container.Get("attribute")
		s.NoError(err)

		value, err := attribute.(etl.Container).Get("value")
		s.NoError(err)
		s.Equal(float64(i+1), value)
	}
}

// TestFissionMergeIntoHandler
func (s *PayloadDecoderSuite) TestFissionMergeDimensionsHandler() {
	payload := define.NewJSONPayloadFrom([]byte(`{"name":"test","values":[{"value":1},{"value":2}]}`), 0)
	s.decoder.FissionMergeDimensionsHandler(true, etl.ExtractByJMESPath("values"))
	containers, err := s.decoder.Decode(payload)
	s.NoError(err)
	s.Len(containers, 2)
	for i, container := range containers {
		dimensions, err := container.Get("dimensions")
		s.NoError(err)

		value, err := dimensions.(etl.Container).Get("value")
		s.NoError(err)
		s.Equal(float64(i+1), value)
	}
}

// TestFissionMergeMetricsHandler
func (s *PayloadDecoderSuite) TestFissionMergeMetricsHandler() {
	payload := define.NewJSONPayloadFrom([]byte(`{"name":"test","values":[{"value":1},{"value":2}]}`), 0)
	s.decoder.FissionMergeMetricsHandler(true, etl.ExtractByJMESPath("values"))
	containers, err := s.decoder.Decode(payload)
	s.NoError(err)
	s.Len(containers, 2)
	for i, container := range containers {
		metrics, err := container.Get("metrics")
		s.NoError(err)

		value, err := metrics.(etl.Container).Get("value")
		s.NoError(err)
		s.Equal(float64(i+1), value)
	}
}

// TestPayloadDecoderSuite
func TestPayloadDecoderSuite(t *testing.T) {
	suite.Run(t, new(PayloadDecoderSuite))
}
