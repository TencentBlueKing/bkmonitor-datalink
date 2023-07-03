// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procstatus

import (
	"reflect"
	"testing"
)

func Test_removeKwargs(t *testing.T) {
	type args struct {
		cmdlineSlice []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			"无参数",
			args{[]string{"a", "b", "c"}},
			[]string{"a", "b", "c"},
		},
		{
			"无等号参数",
			args{[]string{"a", "-b", "c", "d"}},
			[]string{"a", "-b", secretPlaceHolder, "d"},
		},
		{
			"连续无等号参数",
			args{[]string{"a", "-b", "-c", "d", "f"}},
			[]string{"a", "-b", "-c", secretPlaceHolder, "f"},
		},
		{
			"等号参数",
			args{[]string{"a", "--b=c", "d"}},
			[]string{"a", "--b=" + secretPlaceHolder, "d"},
		},
		{
			"有无等号混合参数先等号",
			args{[]string{"a", "--b=c", "-d", "e", "f"}},
			[]string{"a", "--b=" + secretPlaceHolder, "-d", secretPlaceHolder, "f"},
		},
		{
			"有无等号混合参数先无等号",
			args{[]string{"a", "-b", "c", "--d=e", "f"}},
			[]string{"a", "-b", secretPlaceHolder, "--d=" + secretPlaceHolder, "f"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := removeKwargs(tt.args.cmdlineSlice); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("removeKwargs() = %v, want %v", got, tt.want)
			}
		})
	}
}
