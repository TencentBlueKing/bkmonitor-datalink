// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpmiddleware

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"net/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func init() {
	Register("content_decompressor", ContentDecompressor)
}

func ContentDecompressor(_ string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return HTTPContentDecompressor(next)
	}
}

const (
	headerContentEncoding = "Content-Encoding"
	headerValueGZIP       = "gzip"
)

type CompressRoundTripper struct {
	http.RoundTripper
}

func (r *CompressRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get(headerContentEncoding) != "" {
		// If the header already specifies a content encoding then skip compression
		// since we don't want to compress it again. This is a safeguard that normally
		// should not happen since CompressRoundTripper is not intended to be used
		// with http clients which already do their own compression.
		return r.RoundTripper.RoundTrip(req)
	}

	// Gzip the body.
	buf := bytes.NewBuffer([]byte{})
	gzipWriter := gzip.NewWriter(buf)
	_, copyErr := io.Copy(gzipWriter, req.Body)
	closeErr := req.Body.Close()

	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}

	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}

	// Create a new request since the docs say that we cannot modify the "req"
	// (see https://golang.org/pkg/net/http/#RoundTripper).
	cReq, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), buf)
	if err != nil {
		return nil, err
	}

	// Clone the headers and add gzip encoding header.
	cReq.Header = req.Header.Clone()
	cReq.Header.Add(headerContentEncoding, headerValueGZIP)

	return r.RoundTripper.RoundTrip(cReq)
}

type ErrorHandler func(w http.ResponseWriter, r *http.Request, errorMsg string, statusCode int)

type decompressor struct {
	errorHandler ErrorHandler
}

type DecompressorOption func(d *decompressor)

// HTTPContentDecompressor is a middleware that offloads the task of handling compressed
// HTTP requests by identifying the compression format in the "Content-Encoding" header and re-writing
// request body so that the handlers further in the chain can work on decompressed data.
// It supports gzip and deflate/zlib compression.
func HTTPContentDecompressor(h http.Handler, opts ...DecompressorOption) http.Handler {
	d := &decompressor{}
	for _, o := range opts {
		o(d)
	}
	if d.errorHandler == nil {
		d.errorHandler = defaultErrorHandler
	}
	return d.wrap(h)
}

func (d *decompressor) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		source := &sourceBody{Reader: r.Body}
		newBody, err := newBodyReader(r.Header.Get("Content-Encoding"), source)
		if err != nil {
			// A header the decompressor cannot read; not counted when the
			// body below it failed first.
			if source.err == nil {
				define.IncReceiverRejected(define.RequestHttp, define.RejectDecompress)
			}
			d.errorHandler(w, r, err.Error(), http.StatusBadRequest)
			return
		}
		if newBody != nil {
			defer newBody.Close()
			// "Content-Encoding" header is removed to avoid decompressing twice
			// in case the next handler(s) have implemented a similar mechanism.
			r.Header.Del("Content-Encoding")
			// "Content-Length" is set to -1 as the size of the decompressed body is unknown.
			r.Header.Del("Content-Length")
			r.ContentLength = -1
			r.Body = &decompressedBody{ReadCloser: newBody, source: source}
		}
		h.ServeHTTP(w, r)
	})
}

// sourceBody is the request body as it arrived, under the decompressor: it
// keeps the first error a read of it returned other than its end.
type sourceBody struct {
	io.Reader
	err error
}

func (b *sourceBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && b.err == nil {
		b.err = err
	}
	return n, err
}

// decompressedBody is the body the handler reads, through its decompressor.
// The first read that fails for the decompressor's own reason - the stream
// ends before its end, a checksum does not match, what follows is not a
// stream of the encoding - counts the request as decompress once. Most of
// them surface only as the handler reads the body, after the header was
// read. A read that fails because the body below it did - the client gone
// before the body was sent - is not the decompressor's, and is not counted.
type decompressedBody struct {
	io.ReadCloser
	source  *sourceBody
	counted bool
}

func (b *decompressedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && b.source.err == nil && !b.counted {
		b.counted = true
		define.IncReceiverRejected(define.RequestHttp, define.RejectDecompress)
	}
	return n, err
}

// newBodyReader is the decompressor of encoding over body, nil for a body
// not compressed.
func newBodyReader(encoding string, body io.Reader) (io.ReadCloser, error) {
	switch encoding {
	case "gzip":
		gr, err := gzip.NewReader(body)
		if err != nil {
			return nil, err
		}
		return gr, nil
	case "deflate", "zlib":
		zr, err := zlib.NewReader(body)
		if err != nil {
			return nil, err
		}
		return zr, nil
	}
	return nil, nil
}

// defaultErrorHandler writes the error message in plain text.
func defaultErrorHandler(w http.ResponseWriter, _ *http.Request, errMsg string, statusCode int) {
	http.Error(w, errMsg, statusCode)
}
