// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fields

import (
	"strings"
)

const (
	PrefixResource   = "resource."
	PrefixAttributes = "attributes."
	PrefixConst      = "const."
)

type FieldFrom uint8

const (
	FieldFromUnknown FieldFrom = iota
	FieldFromResource
	FieldFromAttributes
	FieldFromMethod
)

func DecodeFieldFrom(s string) (FieldFrom, string) {
	switch {
	case len(s) == 0:
		return FieldFromUnknown, s
	case strings.HasPrefix(s, PrefixResource):
		return FieldFromResource, s[len(PrefixResource):]
	case strings.HasPrefix(s, PrefixAttributes):
		return FieldFromAttributes, s[len(PrefixAttributes):]
	default:
		return FieldFromMethod, s
	}
}

type StringOrSlice []string

func (s StringOrSlice) String() string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func TrimResourcePrefix(keys ...string) StringOrSlice {
	var ret []string
	for _, key := range keys {
		if strings.HasPrefix(key, PrefixResource) {
			ret = append(ret, key[len(PrefixResource):])
		} else {
			ret = append(ret, key)
		}
	}
	return ret
}

func TrimAttributesPrefix(keys ...string) StringOrSlice {
	var ret []string
	for _, key := range keys {
		if strings.HasPrefix(key, PrefixAttributes) {
			ret = append(ret, key[len(PrefixAttributes):])
		} else {
			ret = append(ret, key)
		}
	}
	return ret
}

func TrimPrefix(s string) string {
	if strings.HasPrefix(s, PrefixAttributes) {
		return s[len(PrefixAttributes):]
	}
	if strings.HasPrefix(s, PrefixResource) {
		return s[len(PrefixResource):]
	}
	return s
}
