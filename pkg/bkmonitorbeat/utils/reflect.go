// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"reflect"
)

// RealReflectValue :
func RealReflectValue(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	return v
}

// SafeReflectValueInterface :
func SafeReflectValueInterface(v reflect.Value) interface{} {
	if v.IsValid() {
		return v.Interface()
	}
	return nil
}

// SafeReflectValueAddrInterface :
func SafeReflectValueAddrInterface(v reflect.Value) interface{} {
	if v.IsValid() && v.CanAddr() {
		return SafeReflectValueInterface(v.Addr())
	}
	return nil
}

// GetValueByName :
func GetValueByName(object interface{}, attr string) (reflect.Value, bool) {
	v := RealReflectValue(reflect.ValueOf(object))
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	v = v.FieldByName(attr)
	return v, v.IsValid()
}

// GetPtrByName :
func GetPtrByName(object interface{}, attr string) (interface{}, bool) {
	v, ok := GetValueByName(object, attr)
	if !ok {
		return nil, false
	}
	return SafeReflectValueAddrInterface(v), true
}
