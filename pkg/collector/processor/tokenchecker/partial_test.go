// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tokenchecker

import (
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metrictest"
)

// The token the aes256 checker of these tests decodes, and one it does not.
const (
	decodableToken   = "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
	undecodableToken = "tKm3H4Ran78rWl85HwzfRgw"
)

func tokenPartial(t *testing.T, protocol define.RequestType) float64 {
	t.Helper()
	v, _ := metrictest.Value(t, "bk_collector_receiver_rejected_total", map[string]string{"protocol": protocol.S(), "reason": "token_partial"})
	return v
}

// resources builds a record of rtype with one resource per token, each
// carrying its token in the resource attribute.
func resources(rtype define.RecordType, tokens ...string) any {
	switch rtype {
	case define.RecordTraces:
		data := makeTracesRecord(1, nil)
		data.ResourceSpans().RemoveIf(func(_ ptrace.ResourceSpans) bool { return true })
		for _, token := range tokens {
			makeTracesRecord(1, map[string]string{"bk.data.token": token}).ResourceSpans().MoveAndAppendTo(data.ResourceSpans())
		}
		return data
	case define.RecordMetrics:
		data := makeMetricsRecord(1, nil)
		data.ResourceMetrics().RemoveIf(func(_ pmetric.ResourceMetrics) bool { return true })
		for _, token := range tokens {
			makeMetricsRecord(1, map[string]string{"bk.data.token": token}).ResourceMetrics().MoveAndAppendTo(data.ResourceMetrics())
		}
		return data
	default:
		data := makeLogsRecord(1, nil)
		data.ResourceLogs().RemoveIf(func(_ plog.ResourceLogs) bool { return true })
		for _, token := range tokens {
			makeLogsRecord(1, map[string]string{"bk.data.token": token}).ResourceLogs().MoveAndAppendTo(data.ResourceLogs())
		}
		return data
	}
}

// A request some of whose resources carry a token that does not decode is
// handled with those resources dropped, and counted once as token_partial
// under the protocol it came by. One whose resources all decode is not
// counted, and neither is one whose resources all fail: that one is refused
// whole, which the precheck counts.
func TestARequestWithSomeUndecodableTokensIsCountedOnce(t *testing.T) {
	for _, rtype := range []define.RecordType{define.RecordTraces, define.RecordMetrics, define.RecordLogs} {
		t.Run(rtype.S(), func(t *testing.T) {
			checker := aes256TokenChecker()
			before := tokenPartial(t, define.RequestGrpc)

			all := &define.Record{RecordType: rtype, RequestType: define.RequestGrpc, Data: resources(rtype, decodableToken, decodableToken)}
			_, err := checker.Process(all)
			assert.NoError(t, err)
			assert.Equal(t, before, tokenPartial(t, define.RequestGrpc))

			none := &define.Record{RecordType: rtype, RequestType: define.RequestGrpc, Data: resources(rtype, undecodableToken)}
			_, err = checker.Process(none)
			assert.Equal(t, define.ErrSkipEmptyRecord, errors.Cause(err))
			assert.Equal(t, before, tokenPartial(t, define.RequestGrpc))

			httpBefore := tokenPartial(t, define.RequestHttp)
			some := &define.Record{RecordType: rtype, RequestType: define.RequestGrpc, Data: resources(rtype, decodableToken, undecodableToken, undecodableToken)}
			_, err = checker.Process(some)
			assert.NoError(t, err)
			assert.Equal(t, before+1, tokenPartial(t, define.RequestGrpc))
			assert.Equal(t, httpBefore, tokenPartial(t, define.RequestHttp))
			assert.Equal(t, 1, resourceCount(some))
		})
	}
}

func resourceCount(r *define.Record) int {
	switch r.RecordType {
	case define.RecordTraces:
		return r.Data.(ptrace.Traces).ResourceSpans().Len()
	case define.RecordMetrics:
		return r.Data.(pmetric.Metrics).ResourceMetrics().Len()
	default:
		return r.Data.(plog.Logs).ResourceLogs().Len()
	}
}
