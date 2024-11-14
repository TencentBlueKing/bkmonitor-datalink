// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package random

import (
	"math/rand"
	"time"
	"unsafe"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"lukechampine.com/frand"
)

// String 随机生成指定长度的字符串
func String(n int) string {
	letterRunes := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ._")

	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

// FastString 随机生成指定长度的字符串（效率更高 但内容不可读）
func FastString(n int) string {
	return yoloString(frand.Bytes(n))
}

// Dimensions 随机生成指定长度的维度
func Dimensions(n int) map[string]string {
	rand.Seed(time.Now().UnixNano())
	dims := make(map[string]string)
	for i := 0; i < n; i++ {
		dims[String(12)] = String(24)
	}
	return dims
}

func yoloString(b []byte) string {
	return *((*string)(unsafe.Pointer(&b)))
}

// FastDimensions 快速随机生成指定长度的维度
func FastDimensions(n int) map[string]string {
	dims := make(map[string]string)
	for i := 0; i < n; i++ {
		dims[yoloString(frand.Bytes(12))] = yoloString(frand.Bytes(24))
	}
	return dims
}

// TraceID 随机生成 TraceID
func TraceID() pcommon.TraceID {
	b := make([]byte, 16)
	rand.Read(b)

	ret := [16]byte{}
	for i := 0; i < 16; i++ {
		ret[i] = b[i]
	}
	return pcommon.NewTraceID(ret)
}

// SpanID 随机生成 SpanID
func SpanID() pcommon.SpanID {
	b := make([]byte, 8)
	rand.Read(b)

	ret := [8]byte{}
	for i := 0; i < 8; i++ {
		ret[i] = b[i]
	}
	return pcommon.NewSpanID(ret)
}

// AttributeMap 随机生成指定 key 和类型的 attributeMap
func AttributeMap(keys []string, valueType string) pcommon.Map {
	m := pcommon.NewMap()
	for _, key := range keys {
		switch valueType {
		case "int":
			m.UpsertInt(key, rand.Int63())
		case "bool":
			m.UpsertBool(key, rand.Int31()%2 == 0)
		case "float":
			m.UpsertDouble(key, rand.Float64())
		default:
			m.UpsertString(key, String(24))
		}
	}
	return m
}
