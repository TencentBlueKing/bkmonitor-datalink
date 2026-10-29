// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"math"
	"regexp"
	"sort"
	"strconv"
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

// AuditArgvBytes bounds the command line an execution's audit line carries,
// after redaction; a longer one is cut and ends with AuditArgvTruncated. The
// script on stdin is never in it, only its digest elsewhere on the line.
const (
	AuditArgvBytes     = 1024
	AuditArgvTruncated = " …(truncated)"
)

// AuditArgv is the command line of an execution as its audit line shows it:
// each argument redacted under the same contract as the output and quoted
// when it is empty or holds a space or a quote, the arguments joined by
// spaces, the joined line redacted again as a whole -- so a credential the
// caller split across arguments ("Authorization:" "Bearer" "x") is caught
// as it would be in output -- and the result capped at AuditArgvBytes.
// credential_fields_v1 covers what it covers in output and no more: a flag
// and its value as two arguments ("--password" "x") is not a credential
// field to it, here as there.
func (p *Provider) AuditArgv(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		redacted, _ := p.redactor.bounded(arg, math.MaxInt64)
		if redacted == "" || strings.ContainsAny(redacted, " \t\n\"'") {
			redacted = strconv.Quote(redacted)
		}
		parts = append(parts, redacted)
	}
	line, _ := p.redactor.bounded(strings.Join(parts, " "), math.MaxInt64)
	if len(line) <= AuditArgvBytes {
		return line
	}
	line = line[:AuditArgvBytes]
	for !utf8.ValidString(line) {
		line = line[:len(line)-1]
	}
	return line + AuditArgvTruncated
}

type redactor struct{ secrets []string }

func newRedactor(secrets []string) *redactor {
	copySecrets := append([]string(nil), secrets...)
	sort.Slice(copySecrets, func(i, j int) bool { return len(copySecrets[i]) > len(copySecrets[j]) })
	return &redactor{secrets: copySecrets}
}
func (r *redactor) bounded(value string, limit int64) (string, bool) {
	value = strings.ToValidUTF8(value, "�")
	value = privateKey.ReplaceAllString(value, "<redacted-private-key>")
	// The scheme rule first: run after the field rule, "Authorization: Bearer
	// x" lost only the scheme word to it and kept x.
	value = authCredential.ReplaceAllString(value, "${1} <redacted>")
	value = credentialAssignment.ReplaceAllString(value, "${1}<redacted>")
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
