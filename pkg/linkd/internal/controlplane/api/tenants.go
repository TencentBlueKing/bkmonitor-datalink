// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import "net/http"

func (a *API) listTenants(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// 目录为管理级全局读取，不接受调用者覆盖 bootstrap 租户、用户名或网关地址。
	if r.URL.RawQuery != "" {
		http.Error(w, "tenant list does not accept query parameters", http.StatusBadRequest)
		return
	}
	if a.Tenants == nil {
		http.Error(w, "tenant directory unavailable", http.StatusServiceUnavailable)
		return
	}
	tenants, err := a.Tenants.ListTenants(r.Context())
	if err != nil {
		// 外部错误可能带有敏感上下文，管理接口只返回固定错误。
		http.Error(w, "tenant directory query failed", http.StatusBadGateway)
		return
	}
	output(w, tenants)
}
