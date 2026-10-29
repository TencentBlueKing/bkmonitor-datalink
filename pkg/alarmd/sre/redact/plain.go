// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redact

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// The rules for telling plain configuration from a credential, shared by
// the SRE config view and by alarmd's loader. Accepting a credential
// reference and showing it are separate questions: the loader opens any
// well-formed absolute path (AcceptedCredentialPath), and a path is shown -
// in the view or in an error - only when it also reads as plain
// (ShownCredentialPath), so a value shaped like a key is never echoed.

var (
	pemBlock  = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`)
	jwtLike   = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`)
	base64Run = regexp.MustCompile(`[A-Za-z0-9+/_-]{22,}={0,2}`)
	hexRun    = regexp.MustCompile(`(?i)\b[0-9a-f]{32,}\b`)
	wordSplit = regexp.MustCompile(`[\s,;:/=@?&#()\[\]{}"'<>|.]+`)
)

// CredentialPathLimit is the longest credential reference accepted.
const CredentialPathLimit = 4096

// AcceptedCredentialPath says text is a well-formed path to open as a
// credential file: absolute, free of control characters and of leading or
// trailing whitespace, and at most CredentialPathLimit bytes. The commonest
// mistake with a reference is the credential itself written where its path
// belongs, and a credential almost never starts with "/". It does not ask
// whether the path reads as plain: /etc/secrets/Redis6Password is a path an
// operator may well choose.
func AcceptedCredentialPath(text string) bool {
	return strings.HasPrefix(text, "/") && len(text) <= CredentialPathLimit &&
		strings.TrimSpace(text) == text && !strings.ContainsFunc(text, unicode.IsControl)
}

// ShownCredentialPath says an accepted credential path may also be shown:
// it reads as plain configuration (PlainValue). A path that does not is
// opened but withheld wherever it would be printed.
func ShownCredentialPath(text string) bool {
	return AcceptedCredentialPath(text) && PlainValue(text)
}

// KeyMaterial says text holds something shaped like a key, whatever it is
// called: a PEM block, a JWT, a run of base64 or hex of 16 bytes or more.
func KeyMaterial(text string) bool {
	if pemBlock.MatchString(text) || jwtLike.MatchString(text) || hexRun.MatchString(text) {
		return true
	}
	for _, run := range base64Run.FindAllString(text, -1) {
		if mixedCase(run) && (hasDigit(run) || longestSegment(run) >= 20) {
			return true
		}
	}
	return false
}

// The credential shapes CredentialSpans finds inside a line of text, beside
// the key-material runs KeyMaterial reads.
var (
	// urlUserinfo is the user:password part of a URL (scheme://user:pass@host),
	// the user possibly empty (redis://:pass@host).
	urlUserinfo = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.\-]*://([^/\s:@]*:[^/\s@]+)@`)
	// dsnUserinfo is the user:password of a Go driver DSN, which has no
	// scheme: user:pass@tcp(host:3306)/db.
	dsnUserinfo = regexp.MustCompile(`(?:^|[\s"'=])([^\s:@/"'=]+:[^\s@/"']+)@(?:tcp|udp|unix)\(`)
	// assignedCredential is a credential assigned inside a value, under a
	// name that ends in a credential word: a query parameter (?password=,
	// &access_token=), a JDBC property (;password=), a JAAS option
	// (password="..."), an inline key: value. The name has to end in the
	// word, so password_min_length=8 and token_ttl=60 are not one.
	assignedCredential = regexp.MustCompile(`(?i)(?:^|[\s;,&?{(\["'])["']?[A-Za-z0-9_.\-]*?` + credentialWordPattern + `["']?\s*[=:]\s*("[^"\r\n]*"|'[^'\r\n]*'|[^\s;,&"'}\]]+)`)
	// credentialDirective is a directive that takes a credential as its
	// argument, as redis.conf writes it: requirepass secret, masterauth
	// secret, and the same in prose (password secret).
	credentialDirective = regexp.MustCompile(`(?i)(?:^|\s)(?:requirepass|masterauth|password|passwd|passphrase)\s+([^\s"']+)`)
	// passwordHash is a stored password hash: htpasswd, crypt(3), bcrypt,
	// argon2 ($apr1$, $2y$, $6$, $argon2id$, {SHA}, {SSHA}).
	passwordHash = regexp.MustCompile(`(?:\$(?:apr1|2[abxy]|[156]|argon2(?:id|i|d)?)\$[^\s"']+|\{S?SHA\}[A-Za-z0-9+/=]+)`)
	// pgpassLine is a .pgpass line, host:port:database:user:password; the
	// password is its last field.
	pgpassLine = regexp.MustCompile(`(?m)^[^:\s]+:[0-9*]+:[^:\s]+:[^:\s]+:(\S+)$`)
	// authHeader is an Authorization value: a Bearer or Basic credential, or
	// whatever follows an Authorization: or Authorization= header name.
	authHeader = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+([A-Za-z0-9._~+/=\-]{8,})|\bauthorization\s*[:=]\s*(\S+(?:\s+\S+)?)`)
	// keyPrefix is a token minted by a provider under a known prefix.
	keyPrefix = regexp.MustCompile(`\b(?:(?:AKIA|ASIA)[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9\-]{10,}|sk-[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_\-]{20,})\b`)
)

// CredentialShaped says text holds something shaped like a credential,
// whatever it is called: key material (KeyMaterial), URL userinfo, an
// Authorization or Bearer value, or a token under a known provider prefix.
func CredentialShaped(text string) bool {
	return pemBlock.MatchString(text) || len(CredentialSpans(text)) > 0
}

// CredentialSpans are the byte ranges of text that are shaped like a
// credential, in order and not overlapping: a JWT, a run of hex or base64
// that reads like key material, the userinfo of a URL or a Go DSN, an
// Authorization or Bearer value, a token under a known provider prefix, a
// value assigned to a credential name inside the text, a password hash, a
// .pgpass line's password. Everything outside
// them is ordinary text. A PEM block spans lines and is not found here
// (CredentialShaped reports it).
func CredentialSpans(text string) [][2]int {
	var spans [][2]int
	add := func(start, end int) {
		if start >= 0 && end > start {
			spans = append(spans, [2]int{start, end})
		}
	}
	for _, at := range jwtLike.FindAllStringIndex(text, -1) {
		end := at[1]
		for end < len(text) && strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-", rune(text[end])) {
			end++
		}
		add(at[0], end)
	}
	for _, at := range hexRun.FindAllStringIndex(text, -1) {
		add(at[0], at[1])
	}
	for _, at := range base64Run.FindAllStringIndex(text, -1) {
		if run := text[at[0]:at[1]]; mixedCase(run) && (hasDigit(run) || longestSegment(run) >= 20) {
			add(at[0], at[1])
		}
	}
	for _, at := range urlUserinfo.FindAllStringSubmatchIndex(text, -1) {
		add(at[2], at[3])
	}
	for _, at := range authHeader.FindAllStringSubmatchIndex(text, -1) {
		add(at[2], at[3])
		add(at[4], at[5])
	}
	for _, at := range keyPrefix.FindAllStringIndex(text, -1) {
		add(at[0], at[1])
	}
	for _, rule := range []*regexp.Regexp{dsnUserinfo, assignedCredential, credentialDirective, pgpassLine} {
		for _, at := range rule.FindAllStringSubmatchIndex(text, -1) {
			// A credential name assigned a switch (use_token: on) assigns
			// no secret.
			if rule != dsnUserinfo && rule != pgpassLine && ShownUnderCredentialName(strings.Trim(text[at[2]:at[3]], `"'`)) {
				continue
			}
			add(at[2], at[3])
		}
	}
	for _, at := range passwordHash.FindAllStringIndex(text, -1) {
		add(at[0], at[1])
	}
	return mergeSpans(spans)
}

// mergeSpans sorts spans and joins the ones that overlap or touch.
func mergeSpans(spans [][2]int) [][2]int {
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	merged := spans[:1]
	for _, span := range spans[1:] {
		last := &merged[len(merged)-1]
		if span[0] <= last[1] {
			last[1] = max(last[1], span[1])
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

// A run of base64 characters is key material when it reads like one: mixed
// case with a digit somewhere, or a long stretch with no separator. A path
// such as /etc/coredns/NodeHosts is also a long run of those characters,
// but of short words between slashes and with no digit; 16 random bytes in
// base64 almost never are.
func hasDigit(text string) bool {
	return strings.IndexFunc(text, unicode.IsDigit) >= 0
}

func longestSegment(run string) int {
	longest := 0
	for _, segment := range strings.FieldsFunc(run, func(r rune) bool { return r == '/' || r == '+' || r == '_' || r == '-' }) {
		longest = max(longest, len(segment))
	}
	return longest
}

func mixedCase(text string) bool {
	upper, lower := false, false
	for _, r := range text {
		upper = upper || unicode.IsUpper(r)
		lower = lower || unicode.IsLower(r)
	}
	return upper && lower
}

// generated says a word reads like a generated string rather than a word: a
// password, an identifier minted at random. Short words and words of one
// kind of character (a host label, a number, a constant) are not.
func generated(word string) bool {
	upper, lower, digit := false, false, false
	for _, r := range word {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return len(word) >= 8 && upper && lower && digit
}

// PlainValue says a scalar can be shown: short enough to read, and every
// word of it a word - a host, a path, a duration - and not like a generated
// string.
func PlainValue(text string) bool {
	if len(text) > 512 || strings.ContainsAny(text, "\r\n") || KeyMaterial(text) {
		return false
	}
	for _, word := range wordSplit.Split(text, -1) {
		if generated(word) {
			return false
		}
	}
	return true
}

// credentialWords are the words that name a credential when a name ends in
// one: the last segment of password, db_password, redisPass, requirepass,
// aws_secret_access_key. credentialWordPattern is the same set as a
// pattern, for a name inside text.
var credentialWords = map[string]bool{"pass": true, "passwd": true, "password": true, "pwd": true, "passphrase": true,
	"secret": true, "token": true, "credential": true, "credentials": true, "apikey": true, "cookie": true, "salt": true,
	"requirepass": true, "masterauth": true, "authpass": true, "authorization": true}

// credentialKeyQualifiers make a trailing "key" a credential: api_key,
// access_key, secret_key, private_key, a bare key. Any other key - a cache
// key, a routing key, a partition key - is configuration.
var credentialKeyQualifiers = map[string]bool{"": true, "api": true, "access": true, "secret": true, "private": true,
	"signing": true, "sign": true, "encryption": true, "encrypt": true, "master": true, "admin": true, "client": true,
	"auth": true, "app": true, "license": true, "hmac": true, "aes": true}

// passDirectives make a trailing "pass" a hand-off, not a password: a web
// server's proxy_pass, fastcgi_pass and the like name an upstream.
var passDirectives = map[string]bool{"proxy": true, "fastcgi": true, "uwsgi": true, "scgi": true, "grpc": true, "memcached": true}

const credentialWordPattern = `(?:pass|passwd|password|pwd|passphrase|secret|token|credentials?|api[_-]?key|access[_-]?key|secret[_-]?key|private[_-]?key|client[_-]?secret|requirepass|masterauth|authorization)`

var nameSegment = regexp.MustCompile(`[A-Z]+[a-z0-9]*|[a-z0-9]+`)

// CredentialName says a name holds a credential by what it names: its last
// segment - split at _ . - and camelCase - is a credential word, or a key
// qualified as one (credentialKeyQualifiers). A name that only starts with
// such a word names something about it - password_min_length, token_ttl,
// secretRef, password_file - and is configuration.
func CredentialName(name string) bool {
	segments := nameSegment.FindAllString(name, -1)
	if len(segments) == 0 {
		return false
	}
	last := strings.ToLower(segments[len(segments)-1])
	qualifier := ""
	if len(segments) > 1 {
		qualifier = strings.ToLower(segments[len(segments)-2])
	}
	if last == "pass" && passDirectives[qualifier] {
		return false
	}
	if credentialWords[last] || strings.HasSuffix(last, "password") || strings.HasSuffix(last, "passwd") ||
		strings.HasSuffix(last, "secret") || strings.HasSuffix(last, "token") {
		return true
	}
	if last != "key" && last != "keys" {
		return false
	}
	return credentialKeyQualifiers[qualifier]
}

// booleanLiteral is a whole value that is a switch, not a secret.
var booleanLiteral = regexp.MustCompile(`(?i)^(?:true|false|yes|no|on|off)$`)

// ShownUnderCredentialName says a value under a credential name is not a
// credential and may be shown: the whole value is a boolean literal
// (secure_cookie: true). A number is not exempt: a numeric password or PIN
// is a password.
func ShownUnderCredentialName(text string) bool { return booleanLiteral.MatchString(text) }

// CredentialNamed says some segment of a name is a credential word: the
// password_file, tls_key_path family, whose value is where a credential is
// kept rather than the credential, but which holds the credential itself
// when it is written there by mistake.
func CredentialNamed(name string) bool {
	segments := nameSegment.FindAllString(name, -1)
	for index := range segments {
		if CredentialName(strings.Join(segments[:index+1], "_")) {
			return true
		}
	}
	return false
}
