// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// JSONPayloadSuite :
type JSONPayloadSuite struct {
	suite.Suite
}

// TestUsage :
func (s *JSONPayloadSuite) TestUsage() {
	cases := []map[string]interface{}{
		{
			"number": 1.0,
			"float":  2.3,
			"string": "4",
		},
	}

	for _, c := range cases {
		payload := define.NewJSONPayload(0)
		s.NoError(payload.From(c))
		var output map[string]interface{}
		s.NoError(payload.To(&output))
		for key, value := range c {
			s.Equal(value, output[key])
		}
	}
}

// TestParse :
func (s *JSONPayloadSuite) TestParse() {
	cases := []struct {
		json, name string
		pass       bool
	}{
		{`{"name": "test"}`, "test", true},
		{`{}`, "", true},
		{`[]`, "", false},
	}

	for _, c := range cases {
		var item struct{ Name string }
		var json define.Payload = define.NewJSONPayloadFrom([]byte(c.json), 0)

		err := json.To(&item)
		if c.pass {
			s.Equal(c.name, item.Name)
			s.NoError(err)
		} else {
			s.Error(err)
		}
	}
}

// TestFrom :
func (s *JSONPayloadSuite) TestFrom() {
	cases := []struct {
		json  string
		value interface{}
	}{
		{`"test"`, "test"},
		{`{"Name":"test"}`, &struct{ Name string }{Name: "test"}},
		{`{"name":"test"}`, map[string]string{"name": "test"}},
		{`{"name":"test"}`, map[string]interface{}{"name": "test"}},
		{`1`, 1},
		{`2`, 2.0},
		{`3.4`, 3.4},
		{`[5,6,7]`, []int{5, 6, 7}},
		{`[8,9,"10"]`, []interface{}{8, 9.0, "10"}},
		{`"{\"a\":1}"`, `{"a":1}`},
		{`{"a":1}`, []byte(`{"a":1}`)},
	}

	for _, c := range cases {
		json := define.NewJSONPayload(0)

		err := json.From(c.value)
		s.NoError(err)
		s.Equal([]byte(c.json), []byte(json.Data))
	}
}

// TestDerivePayload :
func (s *JSONPayloadSuite) TestDerivePayload() {
	j := define.NewJSONPayload(0)
	cases := []struct {
		json  string
		value interface{}
	}{
		{`"test"`, "test"},
		{`{"Name":"test"}`, struct{ Name string }{Name: "test"}},
		{`{"name":"test"}`, map[string]string{"name": "test"}},
		{`{"name":"test"}`, map[string]interface{}{"name": "test"}},
		{`1`, 1},
		{`2`, 2.0},
		{`3.4`, 3.4},
		{`[5,6,7]`, []int{5, 6, 7}},
		{`[8,9,"10"]`, []interface{}{8, 9.0, "10"}},
	}

	for _, c := range cases {
		json, err := define.DerivePayload(j, &c.value)

		s.NoError(err)
		s.Equal([]byte(c.json), []byte(json.(*define.JSONPayload).Data))
	}
}

// TestFormat :
func (s *JSONPayloadSuite) TestFormat() {
	payload := define.NewJSONPayload(0)
	payload.Data = []byte(`{"name":"test"}`)
	cases := []struct {
		flag     string
		excepted string
	}{
		{"%v", "#0"},
		{"%+v", `{"name":"test"}`},
		{"%-v", "[123 34 110 97 109 101 34 58 34 116 101 115 116 34 125]"},
		{"%#v", payload.String()},
	}

	for _, c := range cases {
		result := fmt.Sprintf(c.flag, payload)
		s.Equal(c.excepted, result)
	}
}

// TestMeta
func (s *JSONPayloadSuite) TestMeta() {
	payload := define.NewDefaultPayload()

	s.NoError(payload.From(map[string]interface{}{
		"key":   "test",
		"value": 1,
	}))

	meta1 := payload.Meta()
	s.NotNil(meta1)

	meta1.Store("test", 1)
	v1, ok := meta1.Load("test")
	s.True(ok)

	meta2 := payload.Meta()
	s.Equal(meta1, meta2)
	v2, ok := meta2.Load("test")
	s.True(ok)
	s.Equal(v1, v2)

	derived, err := define.DerivePayload(payload, map[string]interface{}{})
	s.NoError(err)

	meta3 := derived.Meta()
	s.Equal(meta1, meta3)
	v3, ok := meta3.Load("test")
	s.True(ok)
	s.Equal(v1, v3)

	meta1.Store("test", 1)
	v1, ok = meta1.Load("test")
	s.True(ok)
	v2, ok = meta1.Load("test")
	s.True(ok)
	v3, ok = meta3.Load("test")
	s.True(ok)

	s.Equal(v1, v2)
	s.Equal(v2, v3)

	noMeta := define.NewDefaultPayload()
	derived, err = define.DerivePayload(noMeta, map[string]interface{}{})
	s.NoError(err)

	meta4 := derived.Meta()
	_, ok = meta4.Load("test")
	s.False(ok)
}

// TestJSONPayloadSuite :
func TestJSONPayloadSuite(t *testing.T) {
	suite.Run(t, new(JSONPayloadSuite))
}
