// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package victoriaMetrics

import (
	"regexp"
	"strings"
)

const http2ClientConnCloseError = "http2: client connection force closed via ClientConn.Close"

var http2GoAwayNoError = regexp.MustCompile("http2: server sent GOAWAY and closed the connection; LastStreamID=[0-9]+, ErrCode=NO_ERROR, debug=")

// vmQuerySyncHTTP2RetryReason 仅识别已复现的连接关闭错误。query_sync 是只读查询，
// 但 HTTP 状态、业务错误、JSON 解析错误及响应上限错误不能重复请求。
func vmQuerySyncHTTP2RetryReason(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if strings.Contains(message, http2ClientConnCloseError) {
		return "client_conn_close"
	}
	if http2GoAwayNoError.MatchString(message) {
		return "goaway_no_error"
	}
	return ""
}
