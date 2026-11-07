// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegisv2

import (
	"errors"
	"mime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/aegis"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

// decodeCollect 是 /collect 的输入分流入口，始终只返回 Traces。
func decodeCollect(contentType string, buf []byte) (ptrace.Traces, error) {
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = mediaType
	}
	if contentType == define.ContentTypeProtobuf {
		// Protobuf 的首字节可能为 0x0a，不能按 JSON 前导换行清理。
		req := ptraceotlp.NewRequest()
		if err := req.UnmarshalProto(buf); err != nil {
			return ptrace.Traces{}, err
		}
		return req.Traces(), nil
	}
	buf = sanitizePayload(buf)
	traces, err := aegis.ConvertJSON(buf)
	if !errors.Is(err, aegis.ErrNotAegis) {
		return traces, err
	}
	req := ptraceotlp.NewRequest()
	if err := req.UnmarshalJSON(buf); err != nil {
		return ptrace.Traces{}, err
	}
	return req.Traces(), nil
}

// sanitizePayload attempts to fix common malformed payload issues
// (e.g., leading delimiters like comma, which can happen with certain proxies or batching)
func sanitizePayload(bs []byte) []byte {
	if len(bs) == 0 {
		return bs
	}
	start := 0
	for start < len(bs) {
		c := bs[start]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' {
			start++
		} else {
			break
		}
	}
	if start > 0 && start < len(bs) {
		return bs[start:]
	}
	return bs
}
