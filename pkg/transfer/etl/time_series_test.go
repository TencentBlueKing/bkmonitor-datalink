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

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// TSSchemaRecordSuite :
type TSSchemaRecordSuite struct {
	suite.Suite
}

func (s *TSSchemaRecordSuite) makeTransformer(name string) func(from etl.Container, to etl.Container) error {
	return func(from etl.Container, to etl.Container) error {
		value, err := from.Get(name)
		s.NoError(err)
		return to.Put(name, value)
	}
}

// TestUsage :
func (s *TSSchemaRecordSuite) TestUsage() {
	ctrl := gomock.NewController(s.T())
	from := etl.NewMapContainer()
	to := etl.NewMapContainer()

	ts := "2019-01-17 15:00:00"
	timeField := NewMockField(ctrl)
	timeField.EXPECT().String().Return("time").AnyTimes()
	timeField.EXPECT().Transform(gomock.Any(), gomock.Any()).DoAndReturn(s.makeTransformer(timeField.String()))
	s.NoError(from.Put(timeField.String(), ts))

	valueFields := make([]etl.Field, 0)
	values := map[string]interface{}{
		"value1": 0.1,
		"value2": 0.5,
	}
	for name := range values {
		f := NewMockField(ctrl)
		f.EXPECT().String().Return(name).AnyTimes()
		f.EXPECT().Transform(gomock.Any(), gomock.Any()).DoAndReturn(s.makeTransformer(f.String()))
		valueFields = append(valueFields, f)
		s.NoError(from.Put(name, values[name]))
	}

	dimensionFields := make([]etl.Field, 0)
	dimensions := map[string]interface{}{
		"tag1": "my",
		"tag2": "test",
	}
	for name := range dimensions {
		f := NewMockField(ctrl)
		f.EXPECT().String().Return(name).AnyTimes()
		f.EXPECT().Transform(gomock.Any(), gomock.Any()).DoAndReturn(s.makeTransformer(f.String()))
		dimensionFields = append(dimensionFields, f)
		s.NoError(from.Put(name, dimensions[name]))
	}

	s.NoError(etl.NewTSSchemaRecord("").AddTime(timeField).AddMetrics(valueFields...).AddDimensions(dimensionFields...).Transform(from, to))

	v, err := to.Get("time")
	s.NoError(err)
	s.Equal(ts, v)

	v, err = to.Get("metrics")
	valueContainer := v.(etl.Container)
	s.NoError(err)
	for name := range values {
		val := values[name]
		result, err := valueContainer.Get(name)
		s.NoError(err)
		s.Equal(result, val)
	}

	v, err = to.Get("dimensions")
	dimensionContainer := v.(etl.Container)
	s.NoError(err)
	for name := range dimensions {
		dim := dimensions[name]
		result, err := dimensionContainer.Get(name)
		s.NoError(err)
		s.Equal(result, dim)
	}

	ctrl.Finish()
}

// TestTSSchemaRecordSuite :
func TestTSSchemaRecordSuite(t *testing.T) {
	suite.Run(t, new(TSSchemaRecordSuite))
}
