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

	"github.com/pkg/errors"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
)

// MapContainerSuite :
type MapContainerSuite struct {
	suite.Suite
}

// TestType :
func (s *MapContainerSuite) TestType() {
	cases := []struct {
		inValue, outValue interface{}
	}{
		{0, 0},
		{nil, nil},
		{"", ""},
		{[]byte{}, []byte{}},
		{make(map[string]interface{}), NewMapContainer()},
		{make(map[string]int), make(map[string]int)},
	}

	for _, c := range cases {
		container := NewMapContainer()
		err := container.Put("x", c.inValue)
		s.NoError(err)
		value, err := container.Get("x")
		s.NoError(err)
		s.IsType(c.outValue, value)
	}
}

// TestSimple :
func (s *MapContainerSuite) TestSimple() {
	container := NewMapContainer()

	cases := []struct {
		putName, getName string
		value            interface{}
		pass             bool
	}{
		{"int", "int", 1, true},
		{"string", "string", "test", true},
		{"object", "object", nil, true},
		{"imaaaa", "imbbbb", nil, false},
		{"array", "array", []byte("abcd"), true},
		{"mapint", "mapint", make(map[string]int), true},
	}

	for _, c := range cases {
		err := container.Put(c.putName, c.value)
		s.NoError(err)
		value, err := container.Get(c.getName)
		if c.pass {
			s.NoError(err, c.getName)
			s.Equal(c.value, value, c.getName)
		} else {
			s.Equal(errors.Cause(err), define.ErrItemNotFound, c.getName)
		}
	}
}

// TestNested :
func (s *MapContainerSuite) TestNested() {
	cases := []struct {
		inValue, outValue interface{}
		pass              bool
	}{
		{NewMapContainer(), NewMapContainer(), true},
		{make(map[string]interface{}), NewMapContainer(), true},
		{make(map[string]int), make(map[string]int), true},
	}

	for _, c := range cases {
		container := NewMapContainer()
		err := container.Put("x", c.inValue)
		s.NoError(err)
		value, err := container.Get("x")
		s.NoError(err)
		s.IsType(c.outValue, value)
	}
}

// TestUsage :
func (s *MapContainerSuite) TestUsage() {
	container := NewMapContainer()
	s.NoError(container.Put("x", 0))
	s.NoError(container.Put("x", 1))
	s.NoError(container.Put("y", 2))
	s.NoError(container.Put("z", 3))

	value, err := container.Get("x")
	s.NoError(err)
	s.Equal(1, value)

	keys := container.Keys()
	s.Contains(keys, "x")
	s.Contains(keys, "y")
	s.Contains(keys, "z")

	s.NoError(container.Del("x"))
	s.Equal(errors.Cause(container.Del("x")), define.ErrItemNotFound)

	keys = container.Keys()
	s.NotContains(keys, "x")
	s.Contains(keys, "y")
	s.Contains(keys, "z")

	_, err = container.Get("x")
	s.Equal(errors.Cause(err), define.ErrItemNotFound)
}

// TestTypeRename :
func (s *MapContainerSuite) TestTypeRename() {
	cases := []struct {
		inValue, outValue interface{}
		pass              bool
	}{
		{NewMapContainer(), NewMapContainer(), true},
		{make(map[string]interface{}), NewMapContainer(), true},
		{make(map[string]int), make(map[string]int), true},
	}

	for _, c := range cases {
		mapstr := make(map[string]interface{})
		mapstr["x"] = c.inValue
		container := MapContainer(mapstr)
		value, err := container.Get("x")
		s.NoError(err)
		s.IsType(c.outValue, value)
	}
}

// TestMapContainer :
func TestMapContainer(t *testing.T) {
	suite.Run(t, new(MapContainerSuite))
}
