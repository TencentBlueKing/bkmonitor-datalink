// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package textregexp

import "testing"

// 固定页面的展示契约，不在单测读取仓库外的前端或文档。
func TestKACPageValidationExamples(t *testing.T) {
	for _, tc := range []struct{ name, pattern, valid, invalid string }{
		{"ip", `\A(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)(?:\.(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)){3}\z`, "10.0.0.1", "999.0.0.1"},
		{"url", `\A[a-zA-Z]+://[^\s]+\z`, "https://example.com", "[://example.com"},
		{"domain", `\A[a-zA-Z0-9][-a-zA-Z0-9]{0,62}(\.[a-zA-Z0-9][-a-zA-Z0-9]{0,62})+\.?\z`, "host.example.com", "bad domain"},
		{"email", `\A[a-zA-Z0-9_]+([-+.][a-zA-Z0-9_]+)*@[a-zA-Z0-9_]+([-.][a-zA-Z0-9_]+)*\.[a-zA-Z0-9_]+([-.][a-zA-Z0-9_]+)*\z`, "name+tag@example.com", "name@example"},
		{"enAndNum", `\A[A-Za-z0-9]+\z`, "CPU12", "CPU12!"},
		{"number", `\A[0-9]+\z`, "123", "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			re, err := Compile(tc.pattern)
			if err != nil {
				t.Fatal(err)
			}
			for input, want := range map[string]bool{tc.valid: true, tc.invalid: false, "": false, " " + tc.valid + " ": false} {
				got, err := re.MatchString(input)
				if err != nil || got != want {
					t.Fatalf("%q: %v %v", input, got, err)
				}
			}
		})
	}
}

func TestKACPageExtractionExamples(t *testing.T) {
	for _, tc := range []struct{ pattern, input, want string }{
		{`([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)`, "设备IP:10.0.0.1", "10.0.0.1"},
		{`(?<=设备GUID\().*?(?=\))`, "离线：设备GUID(A0A4A0-8E-4C-82-D2A2575406)", "A0A4A0-8E-4C-82-D2A2575406"},
		{`([A-Z]*\([\u4e00-\u9fa5]{1,}\))`, "AVG(应用程序内存使用占比) > 0.5", "AVG(应用程序内存使用占比)"},
	} {
		re, err := Compile(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		matches, err := re.FindAll(tc.input, 1)
		if err != nil || len(matches) != 1 || matches[0].Text != tc.want {
			t.Fatalf("%q: %+v %v", tc.pattern, matches, err)
		}
	}
}
