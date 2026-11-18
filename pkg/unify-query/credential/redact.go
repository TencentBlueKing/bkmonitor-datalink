// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package credential

import (
	"net/url"
	"strings"
)

func sensitiveKey(key string) bool {
	k := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	for _, part := range []string{"password", "secret", "token", "authorization", "privatekey", "keypem", "apikey", "accesskey", "credential"} {
		if strings.Contains(k, part) {
			return true
		}
	}
	return k == "headers" || k == "keyfile" || k == "username"
}

func sensitiveQueryKey(key string) bool {
	k := strings.ToLower(key)
	return sensitiveKey(key) || k == "auth" || k == "key"
}

func parsedURL(value string) (*url.URL, error) {
	if !strings.Contains(value, "://") && !strings.HasPrefix(value, "//") {
		value = "//" + value
	}
	return url.Parse(value)
}

// userinfoCutShort is true when the parser found no userinfo but the raw value has an '@': a password
// with an unescaped '/', '?' or '#' ends the authority early, and the parsed host is then made of the
// credentials (http://user:1234/x@host parses as host user:1234). Such a value is treated as carrying
// credentials, the same reading as a value that does not parse at all.
func userinfoCutShort(u *url.URL, value string) bool {
	return u.User == nil && strings.Contains(value, "@")
}

func HasURLCredentials(value string) bool {
	u, err := parsedURL(value)
	if err != nil {
		return strings.Contains(value, "@")
	}
	if u.User != nil || userinfoCutShort(u, value) {
		return true
	}
	for key := range u.Query() {
		if sensitiveQueryKey(key) {
			return true
		}
	}
	return false
}

// RedactURL removes userinfo and credential query parameters from diagnostics.
func RedactURL(value string) string {
	u, err := parsedURL(value)
	if err != nil || userinfoCutShort(u, value) {
		return "[invalid URL]"
	}
	if !HasURLCredentials(value) {
		return value
	}
	u.User = nil
	query := u.Query()
	for key := range query {
		if sensitiveQueryKey(key) {
			query.Set(key, "[redacted]")
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
}

// URLOrigin keeps only scheme://host:port, for metric labels: userinfo, path and query
// are dropped whether or not they look sensitive. A value without a scheme keeps host:port.
func URLOrigin(value string) string {
	if value == "" {
		return ""
	}
	u, err := parsedURL(value)
	if err != nil || u.Host == "" || userinfoCutShort(u, value) {
		return "[invalid URL]"
	}
	if u.Scheme == "" {
		return u.Host
	}
	return u.Scheme + "://" + u.Host
}

// RedactSettings returns a deep copy; authentication consumers keep original values.
func RedactSettings(settings map[string]any) map[string]any {
	return redact(settings).(map[string]any)
}

func redact(value any) any {
	switch v := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(v))
		for key, value := range v {
			if sensitiveKey(key) {
				copy[key] = "[redacted]"
			} else {
				copy[key] = redact(value)
			}
		}
		return copy
	case map[string]string:
		copy := make(map[string]string, len(v))
		for key, value := range v {
			if sensitiveKey(key) {
				copy[key] = "[redacted]"
			} else {
				copy[key] = RedactURL(value)
			}
		}
		return copy
	case []any:
		copy := make([]any, len(v))
		for i := range v {
			copy[i] = redact(v[i])
		}
		return copy
	case []string:
		copy := make([]string, len(v))
		for i := range v {
			copy[i] = RedactURL(v[i])
		}
		return copy
	case string:
		if HasURLCredentials(v) {
			return RedactURL(v)
		}
		return v
	default:
		return value
	}
}
