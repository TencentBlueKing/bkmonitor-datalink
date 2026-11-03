// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redact

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestCredentialEvidence(t *testing.T) {
	s := New("fixture-private-value")
	raw := json.RawMessage(`{"data":{"password":"other-secret","client_secret":"client-credential","api-key":"api-credential","private_key":"private-credential","message":"password='fixture-private-value' Authorization: Bearer example-token api_key=api-text-credential","count":4},"tenant":"tenant-fixture","space":"space-fixture"}`)
	out := s.JSON(raw)
	for _, secret := range []string{"other-secret", "fixture-private-value", "example-token", "client-credential", "api-credential", "private-credential", "api-text-credential"} {
		if strings.Contains(string(out), secret) {
			t.Errorf("credential retained: %s", secret)
		}
	}
	if !json.Valid(out) || !strings.Contains(string(out), `"count":4`) || !strings.Contains(string(out), "space-fixture") {
		t.Fatalf("lost native context: %s", out)
	}
}

func TestFlattenedSpanCredentialFields(t *testing.T) {
	raw := json.RawMessage(`{"attributes.api_key":"flat-api-credential","resource.client_secret":"flat-resource-credential","attributes.http.request.header.Authorization":"flat-auth-credential","resource.service.name":"fixture-service","attributes.token_count":7,"events":[{"attributes":{"exception.api-key":"event-api-credential","message":"fixture-message"}}],"links":[{"attributes.private_key":"link-private-credential"}]}`)
	out := New().JSON(raw)
	for _, credential := range []string{"flat-api-credential", "flat-resource-credential", "flat-auth-credential", "event-api-credential", "link-private-credential"} {
		if strings.Contains(string(out), credential) {
			t.Errorf("flattened Span credential retained: %s", credential)
		}
	}
	for _, retained := range []string{`"resource.service.name":"fixture-service"`, `"attributes.token_count":7`, `"message":"fixture-message"`} {
		if !strings.Contains(string(out), retained) {
			t.Errorf("non-credential Span field changed: %s", out)
		}
	}
}

// URL userinfo is a credential under any scheme; an address without a
// scheme, such as a mail address, is not a URL and is kept.
func TestURLUserinfoIsACredentialUnderAnyScheme(t *testing.T) {
	out := New().Text("redis://user:redis-credential@192.0.2.10:6379 mysql://root:mysql-credential@db:3306/x http://u:http-credential@example.test/ owner ops@example.test")
	for _, credential := range []string{"redis-credential", "mysql-credential", "http-credential"} {
		if strings.Contains(out, credential) {
			t.Errorf("URL credential retained: %s in %s", credential, out)
		}
	}
	if !strings.Contains(out, "ops@example.test") || !strings.Contains(out, "192.0.2.10:6379") {
		t.Fatalf("non-credential text changed: %s", out)
	}
}

// A structure under a credential-named key is walked, not masked whole: a
// reference to where a credential is kept comes through, and a scalar under
// a credential-named key inside it is still masked. A structure that is a
// credential by what it is - headers, credentials, cookies - stays masked.
func TestAStructureUnderACredentialNameIsWalkedNotMasked(t *testing.T) {
	raw := json.RawMessage(`{"secret":{"secretName":"ref-volume","items":[{"key":"ref-key"}],"password":"inline-nested"},"token":"inline-token","headers":{"X-Custom-Auth":"inline-header"},"credentials":{"user":"inline-credential"}}`)
	out := string(New().JSON(raw))
	for _, kept := range []string{"ref-volume", "ref-key"} {
		if !strings.Contains(out, kept) {
			t.Errorf("reference lost: %s in %s", kept, out)
		}
	}
	for _, gone := range []string{"inline-nested", "inline-token", "inline-header", "inline-credential"} {
		if strings.Contains(out, gone) {
			t.Errorf("credential retained: %s in %s", gone, out)
		}
	}
}

// Under a credential-named key a structure is read as a reference: only the
// fields a reference to a Secret has come through, and every other value -
// a list element, a field with an ordinary name - is masked, at any depth.
func TestUnderACredentialNameOnlyAReferenceComesThrough(t *testing.T) {
	for _, raw := range []string{
		`{"token":["sentinel-list"]}`,
		`{"apiKey":{"value":"sentinel-value"}}`,
		`{"password":{"data":"sentinel-data"}}`,
		`{"secret":{"secretName":"ref-kept","items":[{"key":"ref-item","path":"p","note":"sentinel-nested"},"sentinel-element"]}}`,
		`{"password":{"data":{"inner":"sentinel-deep"}}}`,
	} {
		out := string(New().JSON(json.RawMessage(raw)))
		if strings.Contains(out, "sentinel") {
			t.Errorf("%s leaked: %s", raw, out)
		}
		if strings.Contains(raw, "ref-kept") && (!strings.Contains(out, "ref-kept") || !strings.Contains(out, "ref-item")) {
			t.Errorf("%s lost its reference: %s", raw, out)
		}
	}
}

// The reference fields are one table, the same one the CLI holds.
func TestTheReferenceFieldsAreTheSharedTable(t *testing.T) {
	want := []string{"defaultMode", "items", "key", "mode", "name", "namespace", "optional", "path", "secretName"}
	got := slices.Sorted(maps.Keys(referenceFields))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("reference fields %v, want %v", got, want)
	}
}
