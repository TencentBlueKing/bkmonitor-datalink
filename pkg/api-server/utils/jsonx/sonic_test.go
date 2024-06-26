// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jsonx

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type testJson struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestMarshal(t *testing.T) {
	jsonBytes, err := Marshal(testJson{Name: "test", Age: 18})
	assert.Nil(t, err)
	assert.Equal(t, `{"name":"test","age":18}`, string(jsonBytes))
}

func TestUnmarshal(t *testing.T) {
	jsonBytes := []byte(`{"name":"test","age":18}`)
	var test testJson
	err := Unmarshal(jsonBytes, &test)
	assert.Nil(t, err)
	assert.Equal(t, "test", test.Name)
	assert.Equal(t, 18, test.Age)
}

func TestMarshalString(t *testing.T) {
	jsonStr, err := MarshalString(testJson{Name: "test", Age: 18})
	assert.Nil(t, err)
	assert.Equal(t, `{"name":"test","age":18}`, jsonStr)
}

func TestUnmarshalString(t *testing.T) {
	jsonStr := `{"name":"test","age":18}`
	var test testJson
	err := UnmarshalString(jsonStr, &test)
	assert.Nil(t, err)
	assert.Equal(t, "test", test.Name)
	assert.Equal(t, 18, test.Age)
}

func TestMarshalIndent(t *testing.T) {
	jsonBytes, err := MarshalIndent(testJson{Name: "test", Age: 18}, "", "  ")
	assert.Nil(t, err)
	assert.Equal(t, "{\n  \"name\": \"test\",\n  \"age\": 18\n}", string(jsonBytes))
}

func TestDecode(t *testing.T) {
	o := map[string]interface{}{}
	r := strings.NewReader(`{"name":"test","age":18}`)
	err := Decode(r, &o)
	assert.Nil(t, err)
	assert.Equal(t, o["name"].(string), "test")
	assert.Equal(t, o["age"].(float64), 18.0)
}

func TestEncode(t *testing.T) {
	o := map[string]interface{}{
		"name": "test",
		"age":  18,
	}
	w := bytes.NewBuffer(nil)
	err := Encode(w, o)
	assert.Nil(t, err)

	var test testJson
	err = UnmarshalString(w.String(), &test)
	assert.Nil(t, err)
	assert.Equal(t, "test", test.Name)
	assert.Equal(t, 18, test.Age)
}
