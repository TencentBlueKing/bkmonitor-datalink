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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

func benchmarkExtractor(b *testing.B, extractors []etl.ExtractFn) {
	b.StopTimer()
	jsonDemo := `{"store":{"book":[{"category":"reference","author":"Nigel Rees","title":"Sayings of the Century","price":8.95},{"category":"fiction","author":"Evelyn Waugh","title":"Sword of Honour","price":12.99},{"category":"fiction","author":"Herman Melville","title":"Moby Dick","isbn":"0-553-21311-3","price":8.99},{"category":"fiction","author":"J. R. R. Tolkien","title":"The Lord of the Rings","isbn":"0-395-19395-8","price":22.99}],"bicycle":{"color":"red","price":19.95}},"expensive":10}`
	cases := []struct {
		input string
		value interface{}
	}{
		{`{"a":{"b":{"c":[{"d":[0,["1","2"]]},{"d":[3,4]}]}}}`, "1"},
		{`{"a": "foo", "b": "bar", "c": "baz"}`, "foo"},
		{`{"a": {"b": {"c": {"d": "value"}}}}`, "value"},
		{`{"people":[{"first":"James","last":"d"},{"first":"Jacob","last":"e"},{"first":"Jayden","last":"f"},{"missing":"different"}],"foo":{"bar":"baz"}}`, `James`},
		{jsonDemo, 10.0},
		{jsonDemo, 8.95},
		{jsonDemo, "0-395-19395-8"},
		{jsonDemo, []interface{}{8.95, 12.99}},
		{jsonDemo, []interface{}{8.95, 12.99, 8.99}},
		{jsonDemo, []interface{}{8.99, 22.99}},
		{jsonDemo, []interface{}{"Sword of Honour", "The Lord of the Rings"}},
		{jsonDemo, []interface{}{8.95, 12.99, 8.99, 22.99}},
	}
	for i, c := range cases {
		container := etl.NewMapContainer()
		err := json.Unmarshal([]byte(c.input), &container)
		if err != nil {
			panic(err)
		}

		// fmt.Printf("%s %d\n", b.String(), i)
		extractor := extractors[i]
		b.StartTimer()
		value, err := extractor(container)
		b.StopTimer()
		if err != nil {
			panic(err)
		}

		if !reflect.DeepEqual(c.value, value) {
			panic(fmt.Errorf("expect %v but got %v for %v", c.value, value, c.input))
		}
	}
}
