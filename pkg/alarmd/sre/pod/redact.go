// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// RedactionContract identifies the public output policy. It covers credential
// assignments named token/access_token/api_key/password/secret/client_secret/
// authorization/cookie (case-insensitive), HTTP Bearer/Basic credentials,
// URL userinfo, PEM private keys, and explicitly configured literal secrets.
// It does not claim to identify arbitrary business values or unknown secrets;
// diagnostic scripts must omit those values or configure LiteralSecrets.
const RedactionContract = "credential_fields_v1"

var credentialAssignment = regexp.MustCompile(`(?i)(["']?(?:access[_-]?token|api[_-]?key|token|password|secret|client[_-]?secret|authorization|cookie)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`)
var authCredential = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)

// urlCredential is URL userinfo under any scheme - redis://, mysql://,
// amqp:// as well as http(s):// - since connection strings carry it there.
var urlCredential = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/@]+(?::[^\s/@]*)?@`)
var privateKey = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----.*?(?:-----END (?:[A-Z ]+ )?PRIVATE KEY-----|$)`)

type redactor struct{ secrets []string }

func newRedactor(secrets []string) *redactor {
	copySecrets := append([]string(nil), secrets...)
	sort.Slice(copySecrets, func(i, j int) bool { return len(copySecrets[i]) > len(copySecrets[j]) })
	return &redactor{secrets: copySecrets}
}
func (r *redactor) bounded(value string, limit int64) (string, bool) {
	value = strings.ToValidUTF8(value, "�")
	value = privateKey.ReplaceAllString(value, "<redacted-private-key>")
	value = credentialAssignment.ReplaceAllString(value, "${1}<redacted>")
	value = authCredential.ReplaceAllString(value, "${1} <redacted>")
	value = urlCredential.ReplaceAllString(value, "${1}<redacted>@")
	for _, secret := range r.secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	truncated := int64(len(value)) > limit
	if truncated {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value, truncated
}
