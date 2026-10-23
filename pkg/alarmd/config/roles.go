// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// validateEnabled checks the isolated channel's authentication coordinates
// against the cliauth constructor contract. Shared legacy configurations keep
// their existing load behavior: their CLI is assembled by the runtime.
func (c CLIConfig) validateEnabled() error {
	if !c.Enabled() {
		return nil
	}
	validText := func(value string) bool {
		return canonicalText(value) && len(value) <= 256 && utf8.ValidString(value) &&
			!strings.ContainsFunc(value, unicode.IsControl)
	}
	if !validText(c.EnvironmentID) || !validText(c.EnvironmentName) {
		return errors.New("cli environment_id and environment_name are required canonical text, at most 256 bytes")
	}
	u, err := url.Parse(c.PublicBaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errors.New("cli public_base_url must be an HTTP(S) URL without credentials, query or fragment")
	}
	if strings.Contains(u.Path, "\\") {
		return errors.New("cli public_base_url path is invalid")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "\r\n\x00") {
			return errors.New("cli public_base_url path is invalid")
		}
	}
	if len(c.AdminKey) < 32 || len(c.AdminKey) > 256 {
		return errors.New("cli admin_key must contain 32 to 256 printable ASCII bytes without spaces")
	}
	for _, value := range []byte(c.AdminKey) {
		if value < 0x21 || value > 0x7e {
			return errors.New("cli admin_key must contain 32 to 256 printable ASCII bytes without spaces")
		}
	}
	return nil
}
