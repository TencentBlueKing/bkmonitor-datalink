// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package curl

import (
	"bytes"
	"context"
	encodingJson "encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	Get  = "GET"
	Post = "POST"
)

var bufPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 1024))
	},
}

// Options Curl 入参
type Options struct {
	UrlPath string
	Headers map[string]string
	Body    []byte

	UserName string
	Password string

	Timeout time.Duration

	// MaxResponseBytes 限制响应体最大字节数，在 JSON 解码前拒绝超限响应；
	// 非正数表示不限制，以兼容未配置该选项的现有调用方。
	MaxResponseBytes int64
}

type Curl interface {
	WithDecoder(decoder func(ctx context.Context, reader io.Reader, resp any) (int, error))
	Request(ctx context.Context, method string, opt Options, res any) (int, error)
}

// ResponseBodyLimitError 表示 HTTP 响应体超过调用方设置的大小上限。
type ResponseBodyLimitError struct {
	Limit int64
}

func (e *ResponseBodyLimitError) Error() string {
	return fmt.Sprintf("response body exceeds maximum size of %d bytes", e.Limit)
}

// TruncationReason 返回稳定的机器可读原因，供上层接口生成截断元数据。
func (e *ResponseBodyLimitError) TruncationReason() string {
	return "max_response_bytes"
}

// HttpCurl http 请求方法
type HttpCurl struct {
	decoder func(ctx context.Context, reader io.Reader, res any) (int, error)
}

type responseBudgetReader struct {
	reader   io.Reader
	budget   *metadata.ResourceBudget
	reserved int64
}

func (r *responseBudgetReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return r.reader.Read(p)
	}
	// Keep unknown-length responses streaming. Each chunk is admitted before
	// it enters the decoder while avoiding a full per-route reservation.
	const maxReservationChunk = 32 * 1024
	if len(p) > maxReservationChunk {
		p = p[:maxReservationChunk]
	}
	requested := int64(len(p))
	reservation, reserveErr := r.budget.ReserveResponseBytesUpTo(requested)
	if reserveErr != nil && !metadata.IsResourceBudgetError(reserveErr) {
		return 0, reserveErr
	}
	if reservation == 0 {
		// JSON readers commonly issue one final read at the exact boundary.
		// Probe a single byte before turning the tentative limit into a fatal
		// request rejection.
		var probe [1]byte
		n, readErr := io.ReadFull(r.reader, probe[:])
		if n == 0 && (readErr == io.EOF || readErr == io.ErrUnexpectedEOF) {
			return 0, io.EOF
		}
		if n == 0 && readErr != nil {
			return 0, readErr
		}
		var limitErr *metadata.ResourceBudgetError
		if errors.As(reserveErr, &limitErr) {
			return 0, r.budget.Reject(limitErr.Resource, limitErr.Attempted)
		}
		return 0, reserveErr
	}
	p = p[:reservation]
	n, err := r.reader.Read(p)
	if unused := reservation - int64(n); unused > 0 {
		r.budget.ReleaseResponseBytes(unused)
	}
	r.reserved += int64(n)
	return n, err
}

func (c *HttpCurl) WithDecoder(decoder func(ctx context.Context, reader io.Reader, res any) (int, error)) {
	c.decoder = decoder
}

func (c *HttpCurl) Request(ctx context.Context, method string, opt Options, res any) (size int, err error) {
	ctx, span := trace.NewSpan(ctx, "http-curl")
	defer span.End(&err)

	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   opt.Timeout,
	}

	if opt.UrlPath == "" {
		return size, metadata.NewMessage(
			metadata.MsgHttpCurl,
			"%s",
			"url path is empty",
		).Error(ctx, err)
	}

	req, err := http.NewRequestWithContext(ctx, method, opt.UrlPath, bytes.NewBuffer(opt.Body))
	if err != nil {
		return size, metadata.NewMessage(
			metadata.MsgHttpCurl,
			"%s",
			"client new request error",
		).Error(ctx, err)
	}

	if opt.UserName != "" {
		req.SetBasicAuth(opt.UserName, opt.Password)
	}

	span.Set("req-http-method", method)
	span.Set("req-http-path", opt.UrlPath)

	metadata.NewMessage(
		metadata.MsgHttpCurl,
		"%s [%s] body_bytes: %d",
		method, opt.UrlPath, len(opt.Body),
	).Info(ctx)

	for k, v := range opt.Headers {
		if k != "" && v != "" {
			req.Header.Set(k, v)
		}
	}

	roundTripStarted := time.Now()
	resp, err := client.Do(req)
	span.Set("response-headers-duration", time.Since(roundTripStarted))
	if err != nil {
		return size, HandleClientError(ctx, metadata.MsgHttpCurl, opt.UrlPath, err)
	}

	buf := bufPool.Get().(*bytes.Buffer)
	defer func() {
		_ = resp.Body.Close()
		buf.Reset()
		bufPool.Put(buf)
	}()

	if resp.StatusCode != http.StatusOK {
		return size, metadata.NewMessage(
			metadata.MsgHttpCurl,
			"http code error: %s in %s",
			resp.Status, opt.UrlPath,
		).Error(ctx, err)
	}

	bodyLimit := opt.MaxResponseBytes
	resourceBudget := metadata.GetResourceBudget(ctx)
	var reservedResponseBytes int64
	var streamingBudgetReader *responseBudgetReader
	keepResponseReservation := false
	if resourceBudget != nil && bodyLimit > 0 {
		if resp.ContentLength > bodyLimit {
			return size, resourceBudget.Reject(metadata.ResourceResponseBytes, resp.ContentLength)
		}
		if resp.ContentLength > 0 {
			reservedResponseBytes = resp.ContentLength
			if err = resourceBudget.ReserveResponseBytes(reservedResponseBytes); err != nil {
				return size, err
			}
			// With a known response size, reserve the request and process
			// capacity before the first body byte is read.
			bodyLimit = reservedResponseBytes
		} else {
			streamingBudgetReader = &responseBudgetReader{
				reader: resp.Body,
				budget: resourceBudget,
			}
		}
		defer func() {
			if !keepResponseReservation {
				resourceBudget.ReleaseResponseBytes(reservedResponseBytes)
				if streamingBudgetReader != nil {
					resourceBudget.ReleaseResponseBytes(streamingBudgetReader.reserved)
				}
			}
		}()
	}

	finishResponseReservation := func() error {
		if resourceBudget == nil {
			return nil
		}
		if streamingBudgetReader != nil {
			keepResponseReservation = true
			return nil
		}
		if reservedResponseBytes == 0 {
			return nil
		}
		if int64(size) > reservedResponseBytes {
			return resourceBudget.Reject(metadata.ResourceResponseBytes, int64(size))
		}
		if unused := reservedResponseBytes - int64(size); unused > 0 {
			resourceBudget.ReleaseResponseBytes(unused)
			reservedResponseBytes = int64(size)
		}
		keepResponseReservation = true
		return nil
	}

	if c.decoder != nil {
		decodeStarted := time.Now()
		reader := io.Reader(resp.Body)
		if streamingBudgetReader != nil {
			reader = streamingBudgetReader
		}
		if bodyLimit > 0 {
			// 额外读取一个字节，用于区分“恰好达到上限”和“实际已经超限”。
			reader = io.LimitReader(reader, bodyLimit+1)
		}
		size, err = c.decoder(ctx, reader, res)
		span.Set("response-body-decode-duration", time.Since(decodeStarted))
		span.Set("response-body-bytes", size)
		if bodyLimit > 0 && int64(size) > bodyLimit {
			if resourceBudget != nil {
				return size, resourceBudget.Reject(metadata.ResourceResponseBytes, int64(size))
			}
			return size, &ResponseBodyLimitError{Limit: bodyLimit}
		}
		if err != nil {
			return size, err
		}
		return size, finishResponseReservation()
	} else {
		bodyReadStarted := time.Now()
		reader := io.Reader(resp.Body)
		if streamingBudgetReader != nil {
			reader = streamingBudgetReader
		}
		if bodyLimit > 0 {
			// 额外读取一个字节，用于区分“恰好达到上限”和“实际已经超限”。
			reader = io.LimitReader(reader, bodyLimit+1)
		}
		_, err = io.Copy(buf, reader)
		span.Set("response-body-read-duration", time.Since(bodyReadStarted))
		if err != nil {
			return size, err
		}
		size = buf.Len()
		span.Set("response-body-bytes", size)
		if bodyLimit > 0 && int64(size) > bodyLimit {
			if resourceBudget != nil {
				return size, resourceBudget.Reject(metadata.ResourceResponseBytes, int64(size))
			}
			return size, &ResponseBodyLimitError{Limit: bodyLimit}
		}

		// 使用标准库的 json.Decoder，因为需要 UseNumber() 功能
		// sonic 的 Decoder 不支持 UseNumber()，会导致大整数精度丢失
		decodeStarted := time.Now()
		decoder := encodingJson.NewDecoder(buf)
		decoder.UseNumber()
		err = decoder.Decode(&res)
		span.Set("json-decode-duration", time.Since(decodeStarted))
		if err != nil {
			return size, err
		}
		return size, finishResponseReservation()
	}
}
