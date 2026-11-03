// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIntoRecordType(t *testing.T) {
	for _, tc := range []struct {
		input   string
		want    RecordType
		derived bool
	}{
		{input: "rumTraces", want: RecordRumTraces},
		{input: "traces", want: RecordTraces},
		{input: "traces.derived", want: RecordTracesDerived, derived: true},
		{input: "unknown", want: RecordUndefined},
	} {
		t.Run(tc.input, func(t *testing.T) {
			rtype, derived := IntoRecordType(tc.input)
			assert.Equal(t, tc.want, rtype)
			assert.Equal(t, tc.derived, derived)
		})
	}
}
