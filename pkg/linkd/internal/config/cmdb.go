// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package config

// CMDBResource 启用只读 CMDB APIGW；应用凭据和多租户模式由全局 BluekingConfig 提供。
type CMDBResource struct {
	// BaseURL 可覆盖 CMDB 网关前缀；省略时使用 blueking.api_url 下的 bk-cmdb/prod。
	BaseURL string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
}

// Validate 只校验地址覆盖，启用 CMDB 的跨配置依赖由 Config.Validate 校验。
func (c CMDBResource) Validate() error {
	if c.BaseURL == "" {
		return nil
	}
	return validateAPIGWURL(c.BaseURL)
}
