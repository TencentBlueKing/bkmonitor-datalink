// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BluekingConfig 定义部署级蓝鲸调用配置；不改变 Event/Alert 或管理 API 的租户身份。
type BluekingConfig struct {
	EnableMultiTenantMode bool   `yaml:"enable_multi_tenant_mode" json:"enable_multi_tenant_mode"` // EnableMultiTenantMode 控制是否查询租户 bk_admin；缺省使用 admin。
	APIURL                string `yaml:"api_url,omitempty" json:"api_url,omitempty"`               // APIURL 为 APIGW 公共根地址，不包含具体网关名。
	AppCode               string `yaml:"app_code,omitempty" json:"app_code,omitempty"`             // AppCode 为全租户应用代码。
	AppSecret             string `yaml:"app_secret,omitempty" json:"app_secret,omitempty"`         // AppSecret 仅保存在部署配置，展示时脱敏。
}

// Validate 校验成组提供的应用连接；required 为 true 时不能仅声明多租户开关。
func (c BluekingConfig) Validate(required bool) error {
	if !required && c.APIURL == "" && c.AppCode == "" && c.AppSecret == "" {
		return nil
	}
	if err := validateAPIGWURL(c.APIURL); err != nil {
		return fmt.Errorf("blueking.api_url is invalid")
	}
	for _, v := range []struct {
		value string
		max   int
	}{{c.AppCode, 128}, {c.AppSecret, 4096}} {
		if v.value == "" || v.value == redactedSecret || !utf8.ValidString(v.value) || len(v.value) > v.max || strings.TrimSpace(v.value) != v.value || strings.ContainsFunc(v.value, unicode.IsControl) {
			return fmt.Errorf("blueking application credentials are incomplete or invalid")
		}
	}
	return nil
}

// Redacted 返回隐藏应用密钥的值副本，不修改调用方配置。
func (c BluekingConfig) Redacted() BluekingConfig {
	if c.AppSecret != "" {
		c.AppSecret = redactedSecret
	}
	return c
}

func validateAPIGWURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !utf8.ValidString(raw) || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Opaque != "" || u.Fragment != "" || u.RawPath != "" || strings.Contains(u.Path, "..") || len(raw) > 2048 || strings.ContainsAny(raw, "\\#*") || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("invalid APIGW URL")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid APIGW URL port")
		}
	}
	return nil
}
