// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package proxy

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const responseStreamedKey = "unify_response_streamed"

var (
	gatewayStreamPrefix = []byte(`{"result":true,"data":`)
	gatewayStreamSuffix = []byte(`,"message":"success"}`)
)

// JSONStream 按顺序写出一个成功 JSON 响应的片段：调用方编码完一段就写出一段，不在内存中
// 拼出整份响应。首次写出时发送 200 和 JSON 内容类型；代理路径另加与 apiGwResponse 相同的
// 外层，代理不再重复写出。写出受请求取消约束，失败记入 ResponseWriteError。
type JSONStream struct {
	ctx       context.Context
	c         *gin.Context
	started   bool
	closed    bool
	suffix    []byte
	written   int
	err       error
	span      *trace.Span
	writeSpan *trace.Span
}

// NewJSONStream 只记录目标，尚不写出任何内容。
func NewJSONStream(ctx context.Context, c *gin.Context) *JSONStream {
	return &JSONStream{ctx: ctx, c: c}
}

// Started 表示是否已开始写出；开始后响应状态和成功外层都不能再改。
func (s *JSONStream) Started() bool { return s.started }

// Detach 让后续写出不再受请求取消约束，只用于有界的失败收尾，使已开始的响应仍是完整 JSON。
func (s *JSONStream) Detach() { s.ctx = context.WithoutCancel(s.ctx) }

func (s *JSONStream) start() {
	if s.started {
		return
	}
	s.started = true
	s.c.Set(responseStreamedKey, true)
	spanCtx, span := trace.NewSpan(s.ctx, "http-response-encode-write")
	s.span = span
	s.span.Set("segmented", true)
	s.span.Set("streamed", true)
	_, proxied := s.c.Get(ContextConfigUnifyResponseProcess)
	s.span.Set("proxy-response", proxied)
	_, encodeSpan := trace.NewSpan(spanCtx, "http-response-encode")
	var prefix []byte
	if proxied {
		prefix, s.suffix = gatewayStreamPrefix, gatewayStreamSuffix
	}
	encodeSpan.Set("segmented", true)
	encodeSpan.Set("encoded-envelope-bytes", len(prefix)+len(s.suffix))
	var encodeErr error
	encodeSpan.End(&encodeErr)
	_, s.writeSpan = trace.NewSpan(spanCtx, "http-response-write")
	if s.err = s.ctx.Err(); s.err != nil {
		return
	}
	s.c.Status(http.StatusOK)
	render.JSON{}.WriteContentType(s.c.Writer)
	if len(prefix) > 0 {
		s.write(prefix)
	}
}

func (s *JSONStream) write(part []byte) {
	if s.err != nil || len(part) == 0 {
		return
	}
	if s.err = s.ctx.Err(); s.err != nil {
		return
	}
	n, err := s.c.Writer.Write(part)
	s.written += n
	if err == nil && n != len(part) {
		err = io.ErrShortWrite
	}
	s.err = err
}

// Write 依次写出片段，返回首个写出错误；出错后不再写出。
func (s *JSONStream) Write(parts ...[]byte) error {
	if s.closed {
		return s.err
	}
	s.start()
	for _, part := range parts {
		s.write(part)
	}
	return s.err
}

// Close 写出最后的片段和代理外层后缀，结束 trace 并记录写出错误。从未写出时也先发送响应头。
func (s *JSONStream) Close(parts ...[]byte) error {
	if s.closed {
		return s.err
	}
	_ = s.Write(parts...)
	s.write(s.suffix)
	s.closed = true
	if s.err != nil {
		s.c.Set(responseWriteErrorKey, s.err)
	}
	s.writeSpan.Set("writer-body-bytes", s.written)
	s.writeSpan.End(&s.err)
	s.span.Set("http-status", s.c.Writer.Status())
	s.span.Set("writer-body-bytes", s.written)
	s.span.End(&s.err)
	return s.err
}

// responseStreamed 表示 handler 已经自行写出了响应。
func responseStreamed(c *gin.Context) bool { return c.GetBool(responseStreamedKey) }
