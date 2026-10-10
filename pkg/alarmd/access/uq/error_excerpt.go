// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const (
	// errorBodyRead is how much of an answer that was not 200 is read; the
	// rest is left undrained with the connection.
	errorBodyRead = 4096
	// errorExcerptBytes is how much of it an excerpt's body keeps: a
	// provider's reason fits, a request it echoes whole does not. A body cut
	// there is followed by "...", so an excerpt is at most this many bytes
	// and the three of the marker.
	errorExcerptBytes = 384
)

var (
	// credentialPair is a key that names a credential -- any key containing
	// secret, token, password, api key, cookie, ticket or authorization --
	// and the value after it, in JSON, JSON escaped inside a JSON string,
	// form or header shape; a value cut off by the read counts to the end.
	credentialPair = regexp.MustCompile(`(?i)(\\?["']?[\w-]*(?:secret|token|passw(?:or)?d|api_?key|cookie|ticket|authorization)[\w-]*\\?["']?\s*[:=]\s*)` +
		`(\\"(?:[^"\\]|\\[^"])*?\\"|"(?:[^"\\]|\\.)*"|'[^']*'|(?:bearer|basic|token)\s+[^\s,;&}"\\]+|"[^"]*$|[^\s,;&}"\\]+)`)
	// address is where something runs: an IPv4 address, an IPv6 one in
	// brackets, or a dotted host name with a port.
	address     = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?::\d+)?\b|\[[0-9A-Fa-f:.]+\](?::\d+)?|\b[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+:\d{2,5}\b`)
	whitespaces = regexp.MustCompile(`\s+`)
)

// providerErrorExcerpt is the start of an error body, as an operator may
// read it: valid UTF-8, whitespace folded, every URL, credential value and
// address (IPv4, bracketed IPv6, host:port) replaced, its body cut to
// errorExcerptBytes on a rune boundary and the cut marked "...".
func providerErrorExcerpt(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	text := strings.ToValidUTF8(string(body), "?")
	text = whitespaces.ReplaceAllString(text, " ")
	text = credentialPair.ReplaceAllString(text, "${1}<redacted>")
	text = address.ReplaceAllString(text, "<address>")
	text = strings.TrimSpace(observability.SanitizeErrorText(text))
	if len(text) <= errorExcerptBytes {
		return text
	}
	cut := errorExcerptBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}
