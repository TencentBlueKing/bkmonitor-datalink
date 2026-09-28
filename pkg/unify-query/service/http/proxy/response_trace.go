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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const responseWriteErrorKey = "unify_response_write_error"

const traceJSONEncodingKey = "unify_trace_json_encoding"

// TraceJSONEncoding opts the topology route into separate encode/write spans,
// including the final proxy envelope. Other routes retain their renderer.
func TraceJSONEncoding(c *gin.Context) { c.Set(traceJSONEncodingKey, true) }

type tracedJSON struct {
	ctx  context.Context
	data any
}

func (r tracedJSON) WriteContentType(w http.ResponseWriter) {
	render.JSON{}.WriteContentType(w)
}

func (r tracedJSON) Render(w http.ResponseWriter) error {
	r.WriteContentType(w)
	_, encodeSpan := trace.NewSpan(r.ctx, "http-response-encode")
	var body []byte
	err := r.ctx.Err()
	if err == nil {
		body, err = json.Marshal(r.data)
	}
	encodeSpan.Set("encoded-body-bytes", len(body))
	encodeSpan.End(&err)
	if err != nil {
		return err
	}
	_, writeSpan := trace.NewSpan(r.ctx, "http-response-write")
	err = r.ctx.Err()
	var written int
	if err == nil {
		written, err = w.Write(body)
		if err == nil && written != len(body) {
			err = io.ErrShortWrite
		}
	}
	writeSpan.Set("writer-body-bytes", written)
	writeSpan.End(&err)
	return err
}

// WriteJSON 观测实际 JSON 编码与 ResponseWriter 写出，不把代理暂存响应视为已写出。
func WriteJSON(ctx context.Context, c *gin.Context, status int, data any) {
	_, span := trace.NewSpan(ctx, "http-response-encode-write")
	var err error
	defer span.End(&err)
	beforeErrors, beforeBytes := len(c.Errors), c.Writer.Size()
	if beforeBytes < 0 {
		beforeBytes = 0
	}
	if c.GetBool(traceJSONEncodingKey) {
		c.Render(status, tracedJSON{ctx: ctx, data: data})
	} else {
		c.JSON(status, data)
	}
	if len(c.Errors) > beforeErrors {
		err = c.Errors.Last().Err
		c.Set(responseWriteErrorKey, err)
	}
	span.Set("http-status", c.Writer.Status())
	span.Set("writer-body-bytes", c.Writer.Size()-beforeBytes)
	_, proxied := c.Get(ContextConfigUnifyResponseProcess)
	span.Set("proxy-response", proxied)
}

func ResponseWriteError(c *gin.Context) error {
	value, _ := c.Get(responseWriteErrorKey)
	err, _ := value.(error)
	return err
}
