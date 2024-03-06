// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"testing"
)

func TestDiffUtil_compareString(t *testing.T) {
	type args struct {
		srcData    interface{}
		bypassData interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{name: "base-equal", args: args{srcData: interface{}("abc"), bypassData: interface{}("abc")}, want: true, wantErr: false},
		{name: "base-not-equal", args: args{srcData: interface{}("abc"), bypassData: interface{}("ab")}, want: false, wantErr: false},
		{name: "json-map-equal", args: args{srcData: interface{}(`{"a":1,"b":2}`), bypassData: interface{}(`{"b":2,"a":1}`)}, want: true, wantErr: false},
		{name: "json-map-not-equal", args: args{srcData: interface{}(`{"a":1,"b":3}`), bypassData: interface{}(`{"b":2,"a":1}`)}, want: false, wantErr: false},
		{name: "json-list-equal", args: args{srcData: interface{}(`["a","b"]`), bypassData: interface{}(`["b","a"]`)}, want: true, wantErr: false},
		{name: "json-list-not-equal", args: args{srcData: interface{}(`["a","b","c"`), bypassData: interface{}(`["b","a"]`)}, want: false, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DiffUtil{}
			got, err := d.compareString(tt.args.srcData, tt.args.bypassData)
			if (err != nil) != tt.wantErr {
				t.Errorf("compareString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("compareString() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDiffUtil_compareList(t *testing.T) {
	type args struct {
		srcData    interface{}
		bypassData interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{name: "base-equal", args: args{srcData: interface{}([]string{"a", "b", "c"}), bypassData: interface{}([]string{"c", "b", "a"})}, want: true, wantErr: false},
		{name: "base-equal-2", args: args{srcData: interface{}([]string{`{"a": "1", "b": "2"}`, `{"c": "3", "d": "4"}`}), bypassData: interface{}([]string{`{"c": "3", "d": "4"}`, `{"a": "1", "b": "2"}`})}, want: true, wantErr: false},
		{name: "base-not-equal", args: args{srcData: interface{}([]string{"a", "b", "c"}), bypassData: interface{}([]string{"c", "b", "a", "d"})}, want: false, wantErr: false},
		{name: "base-not-equal-2", args: args{srcData: interface{}([]string{`{"a": "1", "b": "2"}`, `{"c": "3", "d": "4"}`}), bypassData: interface{}([]string{`{"c": "3", "d": "4"}`, `{"a": "1", "b": "2"}`, `{"d":5}`})}, want: false, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DiffUtil{}
			got, err := d.compareList(tt.args.srcData, tt.args.bypassData)
			if (err != nil) != tt.wantErr {
				t.Errorf("compareList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("compareList() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDiffUtil_compareHash(t *testing.T) {
	type args struct {
		srcData    interface{}
		bypassData interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{name: "base-equal", args: args{srcData: interface{}(map[string]string{"a": "1", "b": "2"}), bypassData: interface{}(map[string]string{"a": "1", "b": "2"})}, want: true, wantErr: false},
		{name: "base-equal-2", args: args{srcData: interface{}(map[string]string{"a": "1", "b": "2"}), bypassData: interface{}(map[string]string{"b": "2", "a": "1"})}, want: true, wantErr: false},
		{name: "base-not-equal", args: args{srcData: interface{}(map[string]string{"a": "1", "b": "3"}), bypassData: interface{}(map[string]string{"b": "2", "a": "1"})}, want: false, wantErr: false},
		{name: "json-map-equal", args: args{srcData: interface{}(map[string]string{"a": `{"x":1,"y":"2"}`, "b": `{"p":1,"q":2}`}), bypassData: interface{}(map[string]string{"b": `{"p":1,"q":2}`, "a": `{"y":"2","x":1}`})}, want: true, wantErr: false},
		{name: "json-map-not-equal", args: args{srcData: interface{}(map[string]string{"a": `{"x":1,"y":"2"}`, "b": `{"p":1,"q":2}`}), bypassData: interface{}(map[string]string{"b": `{"p":1,"q":2}`, "a": `{"x":1,"z":"2"}`})}, want: false, wantErr: false},
		{name: "json-list-equal", args: args{srcData: interface{}(map[string]string{"a": `[1,2,3]`, "b": `[true, false]`}), bypassData: interface{}(map[string]string{"b": `[false,true]`, "a": `[3,2,1]`})}, want: true, wantErr: false},
		{name: "json-list-not-equal", args: args{srcData: interface{}(map[string]string{"a": `[1,2,4]`, "b": `[true, false]`}), bypassData: interface{}(map[string]string{"b": `[false,true]`, "a": `[3,2,1]`})}, want: false, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DiffUtil{}
			got, err := d.compareHash(tt.args.srcData, tt.args.bypassData)
			if (err != nil) != tt.wantErr {
				t.Errorf("compareHash() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("compareHash() got = %v, want %v", got, tt.want)
			}
		})
	}
}
