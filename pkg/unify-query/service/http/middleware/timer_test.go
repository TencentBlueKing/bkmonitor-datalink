// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package middleware

import (
	"log"
	"testing"

	"bou.ke/monkey"
	"github.com/smartystreets/goconvey/convey"
)

func TestSingleGetInstance(t *testing.T) {
	convey.Convey("测试获取实例IP次数是否为单次", t, func() {
		tests := []struct {
			name    string
			mockIPs []string
			want    string
		}{
			// TODO: Add test cases.
			{
				"多次调用singleGetInstance函数",
				[]string{"127.0.0.1", "127.0.0.2", "127.0.0.3"},
				"127.0.0.1",
			},
		}

		for _, tt := range tests {
			convey.Convey(tt.name, func() {
				for _, mockIP := range tt.mockIPs {
					monkey.Patch(getInstanceip, func() (string, error) {
						return mockIP, nil
					})
					got := singleGetInstance()
					log.Println("mockIP:", mockIP)
					log.Println("got:", got)
					convey.So(got, convey.ShouldContainSubstring, tt.want)
				}
			})
		}
	})
}
