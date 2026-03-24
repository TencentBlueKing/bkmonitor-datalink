// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package resulttable

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
)

func TestResultTableFieldOption_InterfaceValue(t *testing.T) {
	type fields struct {
		OptionBase models.OptionBase
		TableID    string
		FieldName  string
		Name       string
	}
	tests := []struct {
		name    string
		fields  fields
		want    any
		wantErr bool
	}{
		{name: "string", fields: fields{OptionBase: models.OptionBase{ValueType: "string", Value: "abcd"}}, want: func() any { return "abcd" }(), wantErr: false},
		{name: "bool_true", fields: fields{OptionBase: models.OptionBase{ValueType: "bool", Value: "true"}}, want: func() any { return true }(), wantErr: false},
		{name: "bool_false", fields: fields{OptionBase: models.OptionBase{ValueType: "bool", Value: "false"}}, want: func() any { return false }(), wantErr: false},
		{name: "list", fields: fields{OptionBase: models.OptionBase{ValueType: "list", Value: "[1,2]"}}, want: func() any { return []any{float64(1), float64(2)} }(), wantErr: false},
		{name: "dict", fields: fields{OptionBase: models.OptionBase{ValueType: "list", Value: `{"k":"v"}`}}, want: func() any { return map[string]any{"k": "v"} }(), wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ResultTableFieldOption{
				OptionBase: tt.fields.OptionBase,
				TableID:    tt.fields.TableID,
				FieldName:  tt.fields.FieldName,
				Name:       tt.fields.Name,
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
