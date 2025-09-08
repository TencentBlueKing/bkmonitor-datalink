// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"reflect"
	"testing"
	"time"
)

func TestParseOptionValue(t *testing.T) {
	type args struct {
		value any
	}
	tests := []struct {
		name    string
		args    args
		want    string
		want1   string
		wantErr bool
	}{
		{name: "int", args: args{1}, want: "1", want1: "int", wantErr: false},
		{name: "float", args: args{1.1}, want: "1.1", want1: "int", wantErr: false},
		{name: "string", args: args{"abc"}, want: "abc", want1: "string", wantErr: false},
		{name: "sliceInt", args: args{[]int{1, 2, 3}}, want: "[1,2,3]", want1: "list", wantErr: false},
		{name: "sliceString", args: args{[]string{"a", "b", "c"}}, want: `["a","b","c"]`, want1: "list", wantErr: false},
		{name: "map[string]", args: args{map[string]any{"a": 1, "b": []int{1, 2}}}, want: `{"a":1,"b":[1,2]}`, want1: "dict", wantErr: false},
		{name: "bool", args: args{true}, want: `true`, want1: "bool", wantErr: false},
		{name: "nil", args: args{nil}, want: ``, want1: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := ParseOptionValue(tt.args.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseOptionValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseOptionValue() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("ParseOptionValue() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptionBase_InterfaceValue(t *testing.T) {
	type fields struct {
		ValueType  string
		Value      string
		Creator    string
		CreateTime time.Time
	}
	tests := []struct {
		name    string
		fields  fields
		want    any
		wantErr bool
	}{
		{name: "string", fields: fields{ValueType: "string", Value: "abcd"}, want: any("abcd"), wantErr: false},
		{name: "bool-true", fields: fields{ValueType: "bool", Value: "true"}, want: any(true), wantErr: false},
		{name: "bool-false", fields: fields{ValueType: "bool", Value: "false"}, want: any(false), wantErr: false},
		{name: "int", fields: fields{ValueType: "int", Value: "123"}, want: any(float64(123)), wantErr: false},
		{name: "list", fields: fields{ValueType: "list", Value: `["a","b"]`}, want: any([]any{"a", "b"}), wantErr: false},
		{name: "dict", fields: fields{ValueType: "dict", Value: `{"a":"aa","b":true}`}, want: any(map[string]any{"a": "aa", "b": true}), wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &OptionBase{
				ValueType:  tt.fields.ValueType,
				Value:      tt.fields.Value,
				Creator:    tt.fields.Creator,
				CreateTime: tt.fields.CreateTime,
			}
			got, err := r.InterfaceValue()
			if (err != nil) != tt.wantErr {
				t.Errorf("InterfaceValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("InterfaceValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}
