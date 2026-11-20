// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegis_test

import (
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/aegis"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertJSONStandalone(t *testing.T) {
	data, err := os.ReadFile("../../testdata/resource.json")
	require.NoError(t, err)
	traces, err := aegis.ConvertJSON(data)
	require.NoError(t, err)
	require.Equal(t, 1, traces.SpanCount())
	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "browser.resource", span.Name())
}

func TestConvertJSONDistinguishesProtocolMismatchFromMalformedInput(t *testing.T) {
	for _, data := range []string{`{}`, `{"resourceSpans":[]}`} {
		_, err := aegis.ConvertJSON([]byte(data))
		require.ErrorIs(t, err, aegis.ErrNotAegis)
	}
	for _, data := range []string{
		`{"topic":`,
		`{"topic":"SDK-test","bean":42,"resourceSpans":[]}`,
		`{"topic":"SDK-test","d2":{},"resourceSpans":[]}`,
		`{"topic":"SDK-test","d2":[{"fields":"bad"}]}`,
	} {
		_, err := aegis.ConvertJSON([]byte(data))
		require.Error(t, err)
		assert.NotErrorIs(t, err, aegis.ErrNotAegis)
	}
}
