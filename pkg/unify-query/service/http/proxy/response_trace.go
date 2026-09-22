// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package proxy

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const responseWriteErrorKey = "unify_response_write_error"

// WriteJSON 观测实际 JSON 编码与 ResponseWriter 写出，不把代理暂存响应视为已写出。
func WriteJSON(ctx context.Context, c *gin.Context, status int, data any) {
	_, span := trace.NewSpan(ctx, "http-response-encode-write")
	var err error
	defer span.End(&err)
	beforeErrors, beforeBytes := len(c.Errors), c.Writer.Size()
	if beforeBytes < 0 {
		beforeBytes = 0
	}
	c.JSON(status, data)
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
