// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"crypto/tls"
	"io"
	"net/http"
)

// ESWriter :
type ESWriter struct {
	transport Transport
}

func (w *ESWriter) getBodyByRecords(records Records) (io.Reader, error) {
	return records.AsBody()
}

// Close :
func (w *ESWriter) Close() error {
	return nil
}

// NewESWriter :
func NewESWriter(transport Transport) *ESWriter {
	return &ESWriter{
		transport: transport,
	}
}

// NewTransportWithTLS 基于 DefaultTransport 创建一个带有 TLS 配置的 Transport
// insecureSkipVerify 为 true 时跳过服务端证书校验
func NewTransportWithTLS(insecureSkipVerify bool) *http.Transport {
	t, ok := DefaultTransport.(*http.Transport)
	if !ok {
		t = http.DefaultTransport.(*http.Transport)
	}
	cloned := t.Clone()
	if cloned.TLSClientConfig == nil {
		cloned.TLSClientConfig = &tls.Config{}
	}
	cloned.TLSClientConfig.InsecureSkipVerify = insecureSkipVerify
	return cloned
}

// DefaultTransport 默认使用 http.DefaultTransport
var DefaultTransport = http.DefaultTransport
