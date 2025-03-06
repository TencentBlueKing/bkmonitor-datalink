// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beater

import (
	gojson "github.com/goccy/go-json"
	jsoniter "github.com/json-iterator/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
)

func registerGseMarshalFunc(lib string) {
	switch lib {
	case "gojson":
		gse.MarshalFunc = goJsonImpl.Marshal
	case "jsoniter":
		gse.MarshalFunc = jsonIteratorImpl.Marshal
	}
}

type goJson struct{}

var goJsonImpl goJson

func (goJson) Marshal(v interface{}) ([]byte, error) {
	return gojson.MarshalWithOption(v, gojson.UnorderedMap(), gojson.DisableHTMLEscape())
}

type jsonIterator struct{}

var jsonIteratorImpl jsonIterator

func (jsonIterator) Marshal(v interface{}) ([]byte, error) {
	return jsoniter.Marshal(v)
}
