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
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// SimpleRecordSuite :
type SimpleRecordSuite struct {
	suite.Suite
}

// TestLazyTransform :
func (s *SimpleRecordSuite) TestLazyTransform() {
	cases := []struct {
		fields []int
		result []int
	}{
		{[]int{}, []int{0}},
		{[]int{1}, []int{1, 1}},
		{[]int{-1}, []int{1, 1}},
		{[]int{1, 2}, []int{1, 2, 2}},
		{[]int{-1, 2}, []int{2, 1, 2}},
		{[]int{1, -2}, []int{1, 2, 2}},
		{[]int{-1, -2}, []int{1, 2, 2}},
		{[]int{2, 1}, []int{2, 1, 2}},
		{[]int{1, -2, 3}, []int{1, 3, 2, 3}},
		{[]int{-4, -5, 1, 2, 3}, []int{1, 2, 3, 4, 5, 5}},
	}

	for _, c := range cases {
		ctrl := gomock.NewController(s.T())
		fields := make([]etl.Field, 0)
		for _, v := range c.fields {
			f := NewMockField(ctrl)
			fields = append(fields, f)
			func(v int) {
				f.EXPECT().Transform(gomock.Any(), gomock.Any()).DoAndReturn(func(from etl.MapContainer, to etl.MapContainer) error {
					if v < 0 {
						v = -v
						return etl.ErrFieldNotReady
					}
					list, err := to.Get("x")
					s.NoError(err)
					s.NoError(to.Put("x", append(list.([]int), v)))
					return nil
				}).MaxTimes(2)
			}(v)
		}

		r := etl.NewSimpleRecord(fields)
		m := etl.NewMapContainer()
		s.NoError(m.Put("x", make([]int, 0, len(fields))))
		fr := NewMockRecord(ctrl) // future test
		fr.EXPECT().String().AnyTimes()
		fr.EXPECT().Transform(gomock.Any(), gomock.Any()).Return(nil)
		fr.EXPECT().Finish().DoAndReturn(func() error {
			v, err := m.Get("x")
			s.NoError(err)
			list := v.([]int)
			s.NoError(m.Put("x", append(list, len(list))))
			return nil
		})
		r.AddRecords(fr)

		s.NoError(r.Transform(nil, m))
		s.NoError(r.Finish())
		list, err := m.Get("x")
		s.NoError(err)
		for i, n := range list.([]int) {
			s.Equal(c.result[i], n, "%+v", c)
		}
		ctrl.Finish()
	}
}

// TestUsage :
func (s *SimpleRecordSuite) TestUsage() {
	ctrl := gomock.NewController(s.T())
	values := map[string]interface{}{
		"this": 1,
		"is":   "2",
		"a":    3.4,
		"test": []int{5, 6},
	}
	from := etl.NewMapContainer()
	to := etl.NewMapContainer()
	fields := make([]etl.Field, 0)

	for name := range values {
		f := NewMockField(ctrl)
		f.EXPECT().String().Return(name).AnyTimes()
		f.EXPECT().Transform(gomock.Any(), gomock.Any()).DoAndReturn(func(from etl.Container, to etl.Container) error {
			name := f.String()
			value, err := from.Get(name)
			s.NoError(err)
			return to.Put(name, value)
		})
		fields = append(fields, f)
		s.NoError(from.Put(name, values[name]))
	}

	record := etl.NewEmptySimpleRecord("test")
	s.NoError(record.AddFields(fields...).Transform(from, to))

	for name := range values {
		value := values[name]
		v1, err := to.Get(name)
		s.NoError(err)
		s.Equal(value, v1)
	}

	ctrl.Finish()
}

// TestSimpleRecordSuite :
func TestSimpleRecordSuite(t *testing.T) {
	suite.Run(t, new(SimpleRecordSuite))
}

// IterationRecordSuite
type IterationRecordSuite struct {
	suite.Suite
}

// TestIndex
func (s *IterationRecordSuite) TestIndex() {
	ctrl := gomock.NewController(s.T())
	defer ctrl.Finish()

	mocked := NewMockRecord(ctrl)
	mocked.EXPECT().Transform(gomock.Any(), gomock.Any()).DoAndReturn(func(from, to etl.Container) error {
		item, err := from.Get("$item")
		s.NoError(err)
		index, err := from.Get("$item_index")
		s.NoError(err)

		s.Equal(item, index)
		return nil
	}).Times(2)

	from := etl.NewMapContainer()
	s.NoError(from.Put("index", []interface{}{0, 1}))
	record := etl.NewIterationRecord("$item", etl.ExtractByPath("index"), mocked)
	s.NoError(record.Transform(from, nil))
}

// TestUsage
func (s *IterationRecordSuite) TestUsage() {
	ctrl := gomock.NewController(s.T())
	defer ctrl.Finish()

	results := map[string]int{}
	mocked := NewMockRecord(ctrl)
	mocked.EXPECT().Transform(gomock.Any(), gomock.Any()).DoAndReturn(func(from, to etl.Container) error {
		key, err := from.Get("$key")
		s.NoError(err)
		value, err := from.Get("$value")
		s.NoError(err)

		results[key.(string)] += value.(int)

		return nil
	}).Times(4)

	record := etl.NewIterationRecord(
		"$key", etl.ExtractByJMESPath("map.*"),
		etl.NewIterationRecord("$value", etl.ExtractByJMESPath("arr"), mocked),
	)

	from := etl.NewMapContainer()
	s.NoError(from.Put("map", map[string]interface{}{
		"a": "b",
		"c": "d",
	}))
	s.NoError(from.Put("arr", []interface{}{1, 2}))

	s.NoError(record.Transform(from, nil))
	s.Len(results, 2)
	s.Equal(3, results["b"])
	s.Equal(3, results["d"])
}

// TestIterationRecordSuite
func TestIterationRecordSuite(t *testing.T) {
	suite.Run(t, new(IterationRecordSuite))
}

// ComplexRecordSuite
type ComplexRecordSuite struct {
	ETLSuite
}

// MakeRecords
func (s *ComplexRecordSuite) MakeRecords(count int) []etl.Record {
	records := make([]etl.Record, 0, count)
	for i := 0; i < cap(records); i++ {
		mocked := NewMockRecord(s.Ctrl)
		records = append(records, mocked)
		func(i int) {
			mocked.EXPECT().Finish().Times(1)
			mocked.EXPECT().Transform(gomock.Any(), gomock.Any()).DoAndReturn(func(from, to etl.Container) error {
				value, err := from.Get("value")
				if err != nil {
					return err
				}
				return to.Put(fmt.Sprintf("%s:%d", value, i), i)
			}).Times(1)
		}(i)
	}
	return records
}

// TestUsage
func (s *ComplexRecordSuite) TestUsage() {
	records := s.MakeRecords(2)
	from := etl.NewMapContainerFrom(map[string]interface{}{
		"value": "x",
	})
	to := etl.NewMapContainer()

	record := etl.NewComplexRecord("", records)
	s.NoError(record.Transform(from, to))
	s.NoError(record.Finish())
	s.Len(from.Keys(), 1)
	s.Len(to.Keys(), len(records))
}

// TestComplexRecordSuite
func TestComplexRecordSuite(t *testing.T) {
	suite.Run(t, new(ComplexRecordSuite))
}

// PrepareRecordSuite
type PrepareRecordSuite struct {
	ComplexRecordSuite
}

// TestUsage
func (s *PrepareRecordSuite) TestUsage() {
	records := s.MakeRecords(2)
	from := etl.NewMapContainerFrom(map[string]interface{}{
		"value": "x",
	})
	to := etl.NewMapContainer()

	record := etl.NewPrepareRecord(records)
	s.NoError(record.Transform(from, to))
	s.NoError(record.Finish())
	s.Len(from.Keys(), len(records)+1)
	s.Len(to.Keys(), 0)
}

// TestPrepareRecordSuite
func TestPrepareRecordSuite(t *testing.T) {
	suite.Run(t, new(PrepareRecordSuite))
}

// ReprocessRecordSuite
type ReprocessRecordSuite struct {
	ComplexRecordSuite
}

// TestUsage
func (s *ReprocessRecordSuite) TestUsage() {
	records := s.MakeRecords(2)
	from := etl.NewMapContainer()
	to := etl.NewMapContainerFrom(map[string]interface{}{
		"value": "x",
	})

	record := etl.NewReprocessRecord(records)
	s.NoError(record.Transform(from, to))
	s.NoError(record.Finish())
	s.Len(from.Keys(), 0)
	s.Len(to.Keys(), len(records)+1)
}

// TestReprocessRecordSuite
func TestReprocessRecordSuite(t *testing.T) {
	suite.Run(t, new(ReprocessRecordSuite))
}

// OptionalRecordSuite
type OptionalRecordSuite struct {
	ETLSuite
}

// TestUsage
func (s *OptionalRecordSuite) TestUsage() {
	record := etl.NewOptionalRecord("x", []etl.Field{
		etl.NewSimpleField("never", etl.ExtractByPath("never"), etl.TransformAsIs),
		etl.NewSimpleField("value", etl.ExtractByPath("value"), etl.TransformAsIs),
		etl.NewSimpleField("number", etl.ExtractByPath("value"), etl.TransformInt),
		etl.NewSimpleFieldWithValue("default", 0, etl.ExtractByPath("value"), etl.TransformInt),
	})

	cases := []struct {
		value  interface{}
		result map[string]interface{}
	}{
		{nil, map[string]interface{}{
			"default": 0,
		}},
		{"x", map[string]interface{}{
			"default": 0,
			"value":   "x",
		}},
		{"1", map[string]interface{}{
			"default": 1,
			"number":  1,
			"value":   "1",
		}},
	}

	for _, c := range cases {
		input := make(map[string]interface{})
		if c.value != nil {
			input["value"] = c.value
		}
		from := etl.NewMapContainerFrom(input)
		to := etl.NewMapContainer()
		s.NoError(record.Transform(from, to))
		result := etl.ContainerToMap(to)
		s.MapEqual(c.result, result)
	}
}

// TestOptionalRecordSuite
func TestOptionalRecordSuite(t *testing.T) {
	suite.Run(t, new(OptionalRecordSuite))
}

// CopyRecordSuite
type CopyRecordSuite struct {
	suite.Suite
}

// TestUsage
func (s *CopyRecordSuite) TestUsage() {
	record := etl.NewCopyRecord()
	from := etl.NewMapContainerFrom(map[string]interface{}{
		"x": 1,
		"y": 2,
	})
	to := etl.NewMapContainer()
	s.NoError(record.Transform(from, to))
	s.Len(to.Keys(), 2)
}

// TestCopyRecordSuite
func TestCopyRecordSuite(t *testing.T) {
	suite.Run(t, new(CopyRecordSuite))
}
