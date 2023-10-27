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
	"github.com/pkg/errors"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// BaseFieldSuite :
type BaseFieldSuite struct {
	suite.Suite
}

// TestUsage :
func (s *BaseFieldSuite) TestUsage() {
	f := etl.NewBaseField("1", 0, false)
	s.Equal("1", f.String())
	v, ok := f.DefaultValue()
	s.Equal(nil, v)
	s.False(ok)

	f = etl.NewBaseField("2", 2, true)
	s.Equal("2", f.String())
	v, ok = f.DefaultValue()
	s.Equal(2, v)
	s.True(ok)
}

// TestBaseFieldSuite :
func TestBaseFieldSuite(t *testing.T) {
	suite.Run(t, new(BaseFieldSuite))
}

// SimpleFieldSuite :
type SimpleFieldSuite struct {
	suite.Suite
	ctrl *gomock.Controller
}

// SetupTest :
func (s *SimpleFieldSuite) SetupTest() {
	s.ctrl = gomock.NewController(s.T())
}

// TearDownTest :
func (s *SimpleFieldSuite) TearDownTest() {
	s.ctrl.Finish()
}

// TestEmptyExtractor :
func (s *SimpleFieldSuite) TestEmptyExtractor() {
	from := etl.NewMapContainer()
	s.NoError(from.Put("x", 1))
	field := etl.NewSimpleField("x", nil, etl.TransformAsIs)
	to := etl.NewMapContainer()
	s.NoError(field.Transform(from, to))
	result, err := to.Get("x")
	s.NoError(err)
	s.Nil(result)
}

// TestEmptyExtractor :
func (s *SimpleFieldSuite) TestEmptyTransformer() {
	from := etl.NewMapContainer()
	s.NoError(from.Put("x", 1))
	field := etl.NewSimpleField("x", etl.ExtractByPath("x"), nil)
	to := etl.NewMapContainer()
	s.NoError(field.Transform(from, to))
	result, err := to.Get("x")
	s.NoError(err)
	s.Equal(1, result)
}

// TestSimpleFieldWithValue :
func (s *SimpleFieldSuite) TestSimpleFieldWithValue() {
	var err error
	cases := []struct {
		name                    string
		extractPath, putPath    []string
		defaults, value, result interface{}
	}{
		{"x", []string{"x", "y", "z"}, []string{"x", "y", "z"}, 0, 0, 0},
		{"x", []string{"x", "y", "z"}, []string{"x", "y"}, 0, 1, 0},
		{"x", []string{"y"}, []string{"z"}, 0, 1, 0},
		{"x", []string{"y"}, []string{"y"}, 0, 1, 1},
	}

	for _, c := range cases {
		field := etl.NewSimpleFieldWithValue(
			c.name, c.defaults, etl.ExtractByPath(c.extractPath...), etl.TransformAsIs,
		)
		to := etl.NewMapContainer()
		from := etl.NewMapContainer()
		item := from
		for _, p := range c.putPath[:len(c.putPath)-1] {
			nextItem := etl.NewMapContainer()
			err = item.Put(p, nextItem)
			s.NoError(err)
			item = nextItem
		}

		err = item.Put(c.putPath[len(c.putPath)-1], c.value)
		s.NoError(err)

		err = field.Transform(from, to)
		s.NoError(err)

		value, err := to.Get(c.name)
		s.NoError(err)
		s.Equal(c.result, value)
	}
}

// TestSimpleField :
func (s *SimpleFieldSuite) TestSimpleField() {
	var err error
	cases := []struct {
		name                 string
		extractPath, putPath []string
		value, result        interface{}
		err                  error
	}{
		{"x", []string{"x", "y", "z"}, []string{"x", "y", "z"}, 0, 0, nil},
		{"x", []string{"x", "y", "z"}, []string{"x", "y"}, 1, nil, etl.ErrExtractTypeUnknown},
		{"x", []string{"y"}, []string{"z"}, 1, nil, define.ErrItemNotFound},
		{"x", []string{"y"}, []string{"y"}, 1, 1, nil},
	}

	for _, c := range cases {
		field := etl.NewSimpleField(
			c.name, etl.ExtractByPath(c.extractPath...), etl.TransformAsIs,
		)
		to := etl.NewMapContainer()
		from := etl.NewMapContainer()
		item := from
		for _, p := range c.putPath[:len(c.putPath)-1] {
			nextItem := etl.NewMapContainer()
			err = item.Put(p, nextItem)
			s.NoError(err)
			item = nextItem
		}

		err = item.Put(c.putPath[len(c.putPath)-1], c.value)
		s.NoError(err)

		err = field.Transform(from, to)
		s.Equal(c.err, errors.Cause(err))

		value, _ := to.Get(c.name)
		s.Equal(c.result, value)
	}
}

// TestSimpleFieldSuite :
func TestSimpleFieldSuite(t *testing.T) {
	suite.Run(t, new(SimpleFieldSuite))
}

// FutureFieldSuite :
type FutureFieldSuite struct {
	suite.Suite
}

// TestNewFutureField :
func (s *FutureFieldSuite) TestNewFutureField() {
	to := etl.NewMapContainer()
	from := etl.NewMapContainer()
	field := etl.NewFutureField("test", func(name string, from etl.Container, to etl.Container) error {
		s.Equal("test", name)
		x, err := from.Get("x")
		s.NoError(err)
		s.NoError(to.Put("y", x))
		return nil
	})
	s.NoError(from.Put("x", 1))
	s.Equal(etl.ErrFieldNotReady, field.Transform(from, to))
	s.NoError(field.Transform(from, to))
	y, err := to.Get("y")
	s.NoError(err)
	s.Equal(1, y)
}

// TestNewFutureFieldWithFn :
func (s *FutureFieldSuite) TestNewFutureFieldWithFn() {
	to := etl.NewMapContainer()
	from := etl.NewMapContainer()
	field := etl.NewFutureFieldWithFn("test", func(name string, to etl.Container) (i interface{}, e error) {
		s.Equal("test", name)
		return 1, nil
	})
	s.Equal(etl.ErrFieldNotReady, field.Transform(from, to))
	s.NoError(field.Transform(from, to))
	v, err := to.Get("test")
	s.NoError(err)
	s.Equal(1, v)
}

// TestFutureFieldSuite :
func TestFutureFieldSuite(t *testing.T) {
	suite.Run(t, new(FutureFieldSuite))
}

// ConstantFieldSuite
type ConstantFieldSuite struct {
	suite.Suite
}

// TestUsage
func (s *ConstantFieldSuite) TestUsage() {
	cases := []interface{}{
		1, 2.3, "4", '5',
	}

	for i, c := range cases {
		field := etl.NewConstantField("x", c)
		to := etl.NewMapContainer()
		from := etl.NewMapContainer()

		s.NoError(field.Transform(from, to), i)
		v, err := to.Get("x")
		s.NoError(err)
		s.Equal(c, v, i)
	}
}

// TestConstantFieldSuite
func TestConstantFieldSuite(t *testing.T) {
	suite.Run(t, new(ConstantFieldSuite))
}

// InitialFieldSuite
type InitialFieldSuite struct {
	testsuite.ETLSuite
}

// TestUsage
func (s *InitialFieldSuite) TestUsage() {
	field := etl.NewInitialField("value", etl.ExtractByJMESPath("value"), etl.TransformAsIs)
	cases := []struct {
		container map[string]interface{}
		value     int
	}{
		{map[string]interface{}{}, 0},
		{map[string]interface{}{
			"value": 1,
		}, 1},
	}

	for i, c := range cases {
		from := etl.NewMapContainerFrom(map[string]interface{}{
			"value": 0,
		})
		to := etl.NewMapContainerFrom(c.container)
		s.NoError(field.Transform(from, to), i)
		v, err := to.Get("value")
		s.NoError(err)
		s.Equal(c.value, v, i)
	}
}

// TestInitialField
func TestInitialField(t *testing.T) {
	suite.Run(t, new(InitialFieldSuite))
}
