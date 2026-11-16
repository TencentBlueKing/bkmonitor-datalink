// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package proxy

import "github.com/gin-gonic/gin"

const responseCleanupKey = "unify_response_cleanup"

// DeferResponseCleanup 将资源释放推迟到代理完成响应编码后。代理内部 handler
// 只保存响应对象，不能在 handler 返回时就释放保护这些对象的并发名额。
func DeferResponseCleanup(c *gin.Context, cleanup func()) {
	value, _ := c.Get(responseCleanupKey)
	callbacks, _ := value.([]func())
	c.Set(responseCleanupKey, append(callbacks, cleanup))
}

func runResponseCleanup(c *gin.Context) {
	value, _ := c.Get(responseCleanupKey)
	callbacks, _ := value.([]func())
	for index := len(callbacks) - 1; index >= 0; index-- {
		callbacks[index]()
	}
	c.Set(responseCleanupKey, nil)
}
