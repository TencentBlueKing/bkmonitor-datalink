// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build !windows && !freebsd

package collector

import (
	"reflect"
	"testing"
)

func Test_GetTcp4SocketStatusCount(t *testing.T) {
	out, err := GetTcp4SocketStatusCount()
	if err != nil {
		t.Fatal(err)
	}
	v := reflect.ValueOf(out)
	count := v.NumField()
	if count != 11 {
		t.Fatal("the number of return data is wrong")
	}
	for i := 0; i < count; i++ {
		if v.Field(i).Uint() < 0 {
			t.Fatal("the return data is wrong")
		}
	}
	t.Log(out)
}

func Test_GetTcp4SocketStatusCountByNetlink(t *testing.T) {
	data, err := GetTcp4SocketStatusCountByNetlink()
	if err != nil {
		t.Fatal(err)
	}
	v := reflect.ValueOf(data)
	count := v.NumField()
	if count != 11 {
		t.Fatal("the number of return data is wrong")
	}
	for i := 0; i < count; i++ {
		if v.Field(i).Uint() < 0 {
			t.Fatal("the return data is wrong")
		}
	}
	t.Log(data)
}
