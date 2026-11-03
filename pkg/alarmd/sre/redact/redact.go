// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package redact removes known deployment credentials and common credential
// fields from diagnostic evidence. Arbitrary application content requires its
// own domain masking; this contract does not promise to identify every secret.
package redact

import (
	"encoding/json"
	"regexp"
	"strings"
)

const Contract = "deployment_known_credentials_and_common_fields/v1"

var assignment = regexp.MustCompile(`(?i)(["']?(?:password|passwd|secret|client[_-]?secret|api[_-]?key|private[_-]?key|admin[_-]?key|access[_-]?token|refresh[_-]?token|token|authorization|cookie)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^,\s}\]]+)`)
var bearer = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9+/_~.=-]+`)

// urlCredential is URL userinfo under any scheme: a connection string's
// credentials (redis://, mysql://, http://) sit there.
var urlCredential = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/@"]+(?::[^\s/@"]*)?@`)

type Scrubber struct{ values []string }

func New(values ...string) *Scrubber {
	s := &Scrubber{}
	for _, value := range values {
		if value != "" {
			s.values = append(s.values, value)
		}
	}
	return s
}

func (s *Scrubber) Text(value string) string {
	for _, secret := range s.values {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	value = bearer.ReplaceAllString(value, "[REDACTED credential]")
	value = urlCredential.ReplaceAllString(value, "${1}[REDACTED]@")
	return assignment.ReplaceAllString(value, `${1}"[REDACTED]"`)
}

func (s *Scrubber) JSON(raw json.RawMessage) json.RawMessage {
	var value any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil
	}
	encoded, err := json.Marshal(s.value(value))
	if err != nil {
		return nil
	}
	return encoded
}

func (s *Scrubber) value(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			// Raw Span columns can flatten attributes/resource paths into
			// dotted names. Apply the existing credential-field predicate to
			// the terminal field, just as for its nested JSON representation.
			field := key
			if dot := strings.LastIndexByte(field, '.'); dot >= 0 {
				field = field[dot+1:]
			}
			// A credential-named key masks a scalar; a structure under it is
			// read as a reference to where a credential is kept - {secretName},
			// {name, key} - and only a reference's fields come through. A
			// credential bundle is masked whole.
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(field))
			_, structure := item.(map[string]any)
			if _, list := item.([]any); list {
				structure = true
			}
			switch {
			case credentialBundleField(normalized):
				out[key] = "[REDACTED]"
			case credentialField(normalized) && !structure:
				out[key] = "[REDACTED]"
			case credentialField(normalized):
				out[key] = s.reference(item)
			default:
				out[s.Text(key)] = s.value(item)
			}
		}
		return out
	case []any:
		for i := range v {
			v[i] = s.value(v[i])
		}
		return v
	case string:
		return s.Text(v)
	default:
		return v
	}
}

func credentialField(normalized string) bool {
	switch normalized {
	case "password", "passwd", "secret", "clientsecret", "apikey", "privatekey", "adminkey", "token", "accesstoken", "refreshtoken", "authorization", "proxyauthorization", "xbkapijwt", "identityassertion":
		return true
	}
	return false
}

// credentialBundleField is a key whose structure is a credential by what it
// is: a header set, a credential set, a cookie jar.
func credentialBundleField(normalized string) bool {
	switch normalized {
	case "cookie", "setcookie", "credentials", "headers":
		return true
	}
	return false
}

// referenceFields are the fields a reference to a Secret has: a volume's
// secret {secretName, items [{key, path, mode}], defaultMode, optional}, a key
// reference {name, key, optional}, a ServiceMonitor's {name, key}. The CLI
// holds the same table (alarmd-cli internal/cli output.go).
var referenceFields = map[string]bool{"secretName": true, "name": true, "key": true, "optional": true, "items": true,
	"path": true, "mode": true, "defaultMode": true, "namespace": true}

// reference is a structure under a credential-named key as it is shown: a
// reference's fields, walked the same way, and every other value masked - a
// field with any other name, a list element that is not a structure - so
// nothing under a credential name comes out unless it names where a
// credential is kept.
func (s *Scrubber) reference(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if !referenceFields[key] {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = s.referenceField(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			switch item.(type) {
			case map[string]any, []any:
				out[i] = s.reference(item)
			default:
				out[i] = "[REDACTED]"
			}
		}
		return out
	default:
		return "[REDACTED]"
	}
}

func (s *Scrubber) referenceField(value any) any {
	switch v := value.(type) {
	case map[string]any, []any:
		return s.reference(v)
	case string:
		return s.Text(v)
	default:
		return v
	}
}
