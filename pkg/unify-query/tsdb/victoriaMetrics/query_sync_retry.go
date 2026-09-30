// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package victoriaMetrics

import "strings"

const http2ClientConnCloseError = "http2: client connection force closed via ClientConn.Close"

// isVMQuerySyncHTTP2Close 只识别这次复现的连接级错误；HTTP 状态、业务错误、
// JSON 解析错误和响应上限错误均不应触发重复查询。
func isVMQuerySyncHTTP2Close(err error) bool {
	return err != nil && strings.Contains(err.Error(), http2ClientConnCloseError)
}
