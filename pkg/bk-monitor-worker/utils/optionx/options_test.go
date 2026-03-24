// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package optionx

import (
	"reflect"
	"testing"
	"time"
)

func TestNewOptions(t *testing.T) {
	type args struct {
		params map[string]any
	}
	tests := []struct {
		name string
		args args
		want *Options
	}{
		{name: "empty map", args: args{params: make(map[string]any)}, want: &Options{params: make(map[string]any)}},
		{name: "nil", args: args{params: nil}, want: &Options{params: make(map[string]any)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewOptions(tt.args.params); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewOptions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOptions_AllKeys(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	tests := []struct {
		name   string
		fields fields
		want   []string
	}{
		{name: "[a,b]", fields: fields{params: map[string]any{"a": 1, "b": 2}}, want: []string{"a", "b"}},
		{name: "empty", fields: fields{params: map[string]any{}}, want: nil},
		{name: "nil", fields: fields{params: map[string]any{}}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			if got := o.AllKeys(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AllKeys() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOptions_Get(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   any
		want1  bool
	}{
		{name: "real nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: nil, want1: true},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: nil, want1: false},
		{name: "number", fields: fields{params: map[string]any{"a": 1}}, args: args{key: "a"}, want: any(1), want1: true},
		{name: "string", fields: fields{params: map[string]any{"a": "string"}}, args: args{key: "a"}, want: any("string"), want1: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.Get(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Get() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("Get() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetBool(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: false, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: false, want1: false},
		{name: "true", fields: fields{params: map[string]any{"a": true}}, args: args{key: "a"}, want: true, want1: true},
		{name: "false", fields: fields{params: map[string]any{"a": false}}, args: args{key: "a"}, want: false, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: false, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetBool(tt.args.key)
			if got != tt.want {
				t.Errorf("GetBool() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetBool() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetDuration(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   time.Duration
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: 0, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: 0, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": time.Duration(10)}}, args: args{key: "a"}, want: time.Duration(10), want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: 0, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetDuration(tt.args.key)
			if got != tt.want {
				t.Errorf("GetDuration() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetDuration() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetFloat64(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   float64
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: 0, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: 0, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": 10.1}}, args: args{key: "a"}, want: 10.1, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: 0, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetFloat64(tt.args.key)
			if got != tt.want {
				t.Errorf("GetFloat64() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetFloat64() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetInt(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: 0, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: 0, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": 10}}, args: args{key: "a"}, want: 10, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: 0, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetInt(tt.args.key)
			if got != tt.want {
				t.Errorf("GetInt() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetInt() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetInt64(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int64
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: 0, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: 0, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": int64(10)}}, args: args{key: "a"}, want: 10, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: 0, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetInt64(tt.args.key)
			if got != tt.want {
				t.Errorf("GetInt64() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetInt64() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetInt8(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int8
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: 0, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: 0, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": int8(10)}}, args: args{key: "a"}, want: 10, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: 0, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetInt8(tt.args.key)
			if got != tt.want {
				t.Errorf("GetInt8() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetInt8() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetString(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   string
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: "", want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: "", want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": "10"}}, args: args{key: "a"}, want: "10", want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": true}}, args: args{key: "a"}, want: "", want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetString(tt.args.key)
			if got != tt.want {
				t.Errorf("GetString() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetString() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetStringMap(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   map[string]any
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: nil, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: nil, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": map[string]any{"q": 1}}}, args: args{key: "a"}, want: map[string]any{"q": 1}, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: nil, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetStringMap(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetStringMap() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetStringMap() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetStringMapString(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   map[string]string
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: nil, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: nil, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": map[string]string{"q": "1"}}}, args: args{key: "a"}, want: map[string]string{"q": "1"}, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: nil, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetStringMapString(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetStringMapString() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetStringMapString() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetStringMapStringSlice(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   map[string][]string
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: nil, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: nil, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": map[string][]string{"q": {"1"}}}}, args: args{key: "a"}, want: map[string][]string{"q": {"1"}}, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: nil, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetStringMapStringSlice(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetStringMapStringSlice() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetStringMapStringSlice() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetStringSlice(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []string
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: nil, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: nil, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": []string{"1"}}}, args: args{key: "a"}, want: []string{"1"}, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: nil, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetStringSlice(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetStringSlice() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetStringSlice() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetInterfaceSliceWithString(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []string
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: nil, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": []any{"1"}}}, args: args{key: "a"}, want: []string{"1"}, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: nil, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetInterfaceSliceWithString(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetStringSlice() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetStringSlice() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetTime(t *testing.T) {
	tm := time.Now()
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   time.Time
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: time.Time{}, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: time.Time{}, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": tm}}, args: args{key: "a"}, want: tm, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: time.Time{}, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetTime(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetTime() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetTime() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_GetUint(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   uint
		want1  bool
	}{
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: 0, want1: false},
		{name: "nil", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: 0, want1: false},
		{name: "right", fields: fields{params: map[string]any{"a": uint(10)}}, args: args{key: "a"}, want: 10, want1: true},
		{name: "other", fields: fields{params: map[string]any{"a": "true"}}, args: args{key: "a"}, want: 0, want1: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			got, got1 := o.GetUint(tt.args.key)
			if got != tt.want {
				t.Errorf("GetUint() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetUint() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestOptions_IsSet(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{name: "true", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "a"}, want: true},
		{name: "false", fields: fields{params: map[string]any{"a": nil}}, args: args{key: "b"}, want: false},
		{name: "false_nil", fields: fields{params: nil}, args: args{key: "a"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			if got := o.IsSet(tt.args.key); got != tt.want {
				t.Errorf("IsSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOptions_Set(t *testing.T) {
	type fields struct {
		params map[string]any
	}
	type args struct {
		key   string
		value any
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{name: "set", fields: fields{params: map[string]any{}}, args: args{key: "a", value: "v"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{
				params: tt.fields.params,
			}
			o.Set(tt.args.key, tt.args.value)
			if got := o.IsSet(tt.args.key); got != tt.want {
				t.Errorf("IsSet() = %v, want %v", got, tt.want)
			}
		})
	}
}
