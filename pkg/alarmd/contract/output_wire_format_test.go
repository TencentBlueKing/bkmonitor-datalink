// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "testing"

func TestResolveOutputWireFormat(t *testing.T) {
	for _, tc := range []struct {
		format   string
		revision int64
		want     string
	}{
		{WireFormatStandardRawEvent, 7, WireFormatStandardRawEvent},
		{WireFormatPythonCompatible, 7, WireFormatPythonCompatible},
		{WireFormatPythonCompatible, 0, WireFormatPythonCompatible},
		{WireFormatTriggerEvent, 7, WireFormatStandardRawEvent},
		{WireFormatTriggerEvent, 0, WireFormatStandardRawEvent},
		{"", 7, WireFormatStandardRawEvent},
		{"", 0, WireFormatPythonCompatible},
		{"unknown", 7, "unknown"},
	} {
		if got := ResolveOutputWireFormat(tc.format, tc.revision); got != tc.want {
			t.Errorf("ResolveOutputWireFormat(%q, %d) = %q, want %q", tc.format, tc.revision, got, tc.want)
		}
	}
}
