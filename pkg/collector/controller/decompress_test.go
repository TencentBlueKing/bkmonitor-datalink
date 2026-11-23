// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controller

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

// receivingConfig is lossConfig with the receiver's HTTP server on at
// endpoint, behind the middlewares the collector ships with: the
// decompressor, under the size limit.
func receivingConfig(t *testing.T, endpoint string) string {
	t.Helper()
	content := lossConfig(10*1024*1024, plainTraces)
	disabled := "receiver:\n  disabled: true\n"
	require.Contains(t, content, disabled)
	return strings.Replace(content, disabled, fmt.Sprintf(`receiver:
  disabled: false
  http_server:
    enabled: true
    endpoint: %q
    middlewares:
      - "content_decompressor"
      - "maxbytes;maxRequestBytes=209715200"
`, endpoint), 1)
}

// freeEndpoint is a local address no listener holds.
func freeEndpoint(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	endpoint := l.Addr().String()
	require.NoError(t, l.Close())
	return endpoint
}

// compressed is spans encoded as OTLP protobuf and compressed with
// encoding: gzip, or zlib for deflate and zlib.
func compressed(t *testing.T, encoding string) []byte {
	t.Helper()
	traces := generator.NewTracesGenerator(define.TracesOptions{SpanCount: 4, SpanKind: int(ptrace.SpanKindServer)}).Generate()
	body, err := ptrace.NewProtoMarshaler().MarshalTraces(traces)
	require.NoError(t, err)
	var buf bytes.Buffer
	if encoding == "gzip" {
		zw := gzip.NewWriter(&buf)
		_, err = zw.Write(body)
		require.NoError(t, err)
		require.NoError(t, zw.Close())
	} else {
		zw := zlib.NewWriter(&buf)
		_, err = zw.Write(body)
		require.NoError(t, err)
		require.NoError(t, zw.Close())
	}
	return buf.Bytes()
}

// A traces request whose compressed body does not decompress - cut short,
// or its checksum not matching, which the decompressor finds only as the
// handler reads the body - is counted once as decompress, through the
// collector's own HTTP server, its middlewares and the OTLP handler, for
// gzip and for zlib under both its names; a body that decompresses is not,
// and neither is one whose client went away before sending all of it, which
// fails below the decompressor.
func TestABodyThatDoesNotDecompressIsCountedAsItIsRead(t *testing.T) {
	endpoint := freeEndpoint(t)
	startCollector(t, receivingConfig(t, endpoint))
	url := "http://" + endpoint + "/v1/traces"
	eventually(t, "the receiver listens", func() bool {
		conn, err := net.Dial("tcp", endpoint)
		if err == nil {
			conn.Close()
		}
		return err == nil
	})
	decompress := func() float64 {
		return value(t, "bk_collector_receiver_rejected_total", map[string]string{"protocol": "http", "reason": "decompress"})
	}
	post := func(encoding string, body []byte) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", define.ContentTypeProtobuf)
		req.Header.Set("Content-Encoding", encoding)
		rsp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		rsp.Body.Close()
		return rsp.StatusCode
	}

	before := decompress()
	require.Equal(t, http.StatusOK, post("gzip", compressed(t, "gzip")))
	require.Equal(t, http.StatusOK, post("deflate", compressed(t, "zlib")))
	assert.Equal(t, before, decompress(), "bodies that decompress")

	// The checksum is the last four bytes of either format: gzip's CRC-32
	// before the length, zlib's Adler-32 at the end.
	corrupt := func(body []byte, at int) []byte {
		out := append([]byte(nil), body...)
		out[at] ^= 0xff
		return out
	}
	gz, zl := compressed(t, "gzip"), compressed(t, "zlib")
	for _, c := range []struct {
		name, encoding string
		body           []byte
	}{
		{"gzip cut short", "gzip", gz[:len(gz)-12]},
		{"gzip checksum", "gzip", corrupt(gz, len(gz)-8)},
		{"deflate cut short", "deflate", zl[:len(zl)-12]},
		{"zlib checksum", "zlib", corrupt(zl, len(zl)-1)},
	} {
		was := decompress()
		assert.NotEqual(t, http.StatusOK, post(c.encoding, c.body), c.name)
		assert.Equal(t, was+1, decompress(), c.name)
	}

	// The client goes away halfway through a body it declared longer: the
	// handler's read fails below the decompressor, which is not counted.
	internal := func() float64 {
		return value(t, "bk_collector_receiver_internal_error_total",
			map[string]string{"source": define.SourceOtlp, "protocol": "http", "record_type": "traces"})
	}
	was, failed := decompress(), internal()
	conn, err := net.Dial("tcp", endpoint)
	require.NoError(t, err)
	half := gz[:len(gz)/2]
	_, err = fmt.Fprintf(conn, "POST /v1/traces HTTP/1.1\r\nHost: %s\r\nContent-Type: %s\r\nContent-Encoding: gzip\r\n"+
		"Content-Length: %d\r\n\r\n", endpoint, define.ContentTypeProtobuf, len(gz))
	require.NoError(t, err)
	_, err = conn.Write(half)
	require.NoError(t, err)
	require.NoError(t, conn.(*net.TCPConn).CloseWrite())
	// The body is counted, if at all, by the read the handler's failure
	// comes from, so before the handler counts its own.
	eventually(t, "the handler fails to read the body", func() bool { return internal() > failed })
	conn.Close()
	assert.Equal(t, was, decompress(), "a body its client did not finish sending")
}
