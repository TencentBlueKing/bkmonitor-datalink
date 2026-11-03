// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package pod

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const (
	pemBody     = "MIIEowIBAAKCAQEAx9Lq3RbZtGmHkV2pQwErTyUiOpAsDfGhJkLzXcVbNm1234"
	pemLastLine = "AB=="
)

func pemLines(label string, end bool) []string {
	lines := []string{"-----BEGIN " + label + "-----", pemBody, pemLastLine}
	if end {
		lines = append(lines, "-----END "+label+"-----")
	}
	return lines
}

func shownValue(t *testing.T, key, text string) string {
	t.Helper()
	provider, _, _ := editProvider(t, "")
	masker := &configMasker{fingerprints: provider.newFingerprints()}
	return masker.value(key, text)
}

func assertNoPEM(t *testing.T, shown string) {
	t.Helper()
	for _, hidden := range []string{"BEGIN ", pemBody[:16], pemLastLine, "END "} {
		if strings.Contains(shown, hidden) {
			t.Errorf("%q shown:\n%s", hidden, shown)
		}
	}
}

// A value that does not parse as YAML is read line by line, and its PEM lines
// are masked from BEGIN to END, the short last line too: the plain-line rule
// never sees them.
func TestAPEMInTextThatDoesNotParseIsMaskedByItsSpan(t *testing.T) {
	text := "host: a.example.test\n  bad: indent\nkey: |\n  " + strings.Join(pemLines("RSA PRIVATE KEY", true), "\n  ") + "\nport: 80\n"
	provider, _, _ := editProvider(t, "")
	if _, ok := (&configMasker{fingerprints: provider.newFingerprints()}).document(text); ok {
		t.Fatal("fixture: the text parsed as a document")
	}
	shown := shownValue(t, "runcfg", text)
	assertNoPEM(t, shown)
	if !strings.Contains(shown, "host: a.example.test") || !strings.Contains(shown, "port: 80") {
		t.Errorf("plain lines lost:\n%s", shown)
	}
}

// A PEM block carried in JSON with escaped newlines is one string: masked as
// key material, as a whole one-line value and as a field of a document.
func TestAPEMInAJSONStringIsKeyMaterial(t *testing.T) {
	escaped := strings.Join(pemLines("PRIVATE KEY", true), `\n`)
	if shown := shownValue(t, "key.json", `{"private":"`+escaped+`"}`); !strings.HasPrefix(shown, "<redacted bytes=") {
		t.Errorf("one-line JSON: %s", shown)
	}
	document := "{\n  \"host\": \"a.example.test\",\n  \"material\": \"" + escaped + "\"\n}"
	shown := shownValue(t, "config.json", document)
	assertNoPEM(t, shown)
	if !strings.Contains(shown, "a.example.test") {
		t.Errorf("plain field lost:\n%s", shown)
	}
}

// A PEM block split across scalars of a document - one item per line - is
// not one scalar the leaf rule can see whole: its short last line reads as
// plain alone. Such a value is read line by line instead, where the span is
// masked from BEGIN to END.
func TestAPEMSplitAcrossScalarsIsMaskedWhole(t *testing.T) {
	for name, text := range map[string]string{
		"sequence": "host: a.example.test\nlines:\n  - \"" + strings.Join(pemLines("EC PRIVATE KEY", true), "\"\n  - \"") + "\"\n",
		"mapping": "host: a.example.test\nl1: \"" + pemLines("EC PRIVATE KEY", true)[0] + "\"\nl2: " + pemBody + "\nl3: " + pemLastLine +
			"\nl4: \"" + pemLines("EC PRIVATE KEY", true)[3] + "\"\n",
		// The BEGIN line in a comment, which a document drops unread; the
		// rest of the block as values.
		"begin in a comment": "host: a.example.test\n# " + pemLines("EC PRIVATE KEY", true)[0] + "\nl2: " + pemBody + "\nl3: " + pemLastLine +
			"\nl4: \"" + pemLines("EC PRIVATE KEY", true)[3] + "\"\n",
		// Both its BEGIN and its END in comments: no scalar holds either.
		"begin and end in comments": "host: a.example.test\n# " + pemLines("EC PRIVATE KEY", true)[0] + "\nl2: " + pemBody + "\nl3: " + pemLastLine +
			"\n# " + pemLines("EC PRIVATE KEY", true)[3] + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			shown := shownValue(t, "runcfg", text)
			assertNoPEM(t, shown)
			if !strings.Contains(shown, "a.example.test") {
				t.Errorf("plain setting lost:\n%s", shown)
			}
		})
	}
}

// Every document of a multi-document YAML value is shown, each decided leaf
// by leaf; none is dropped.
func TestEveryDocumentOfAMultiDocumentValueIsShown(t *testing.T) {
	text := "host: a.example.test\nport: 80\n---\nmirror: b.example.test\nprivate_key: |\n  " + strings.Join(pemLines("PRIVATE KEY", true), "\n  ") + "\n"
	shown := shownValue(t, "runcfg", text)
	assertNoPEM(t, shown)
	for _, kept := range []string{"a.example.test", "port: 80", "mirror: b.example.test", "private_key:"} {
		if !strings.Contains(shown, kept) {
			t.Errorf("lost %q:\n%s", kept, shown)
		}
	}
}

// Unusual labels open the span as any other, and a block with no END line
// masks the text to its end.
func TestAPEMSpanTakesAnyLabelAndRunsToTheEndWithoutAnEND(t *testing.T) {
	for _, label := range []string{"EC PRIVATE KEY", "OPENSSH PRIVATE KEY", "ENCRYPTED PRIVATE KEY", "PGP PRIVATE KEY BLOCK"} {
		for _, end := range []bool{true, false} {
			text := "upstream mirror.example.test\n" + strings.Join(pemLines(label, end), "\n") + "\ncache 30\n"
			shown := shownValue(t, "Corefile", text)
			assertNoPEM(t, shown)
			if !strings.Contains(shown, "upstream mirror.example.test") {
				t.Errorf("%s end=%v: plain line before the block lost:\n%s", label, end, shown)
			}
			if !end && strings.Contains(shown, "cache 30") {
				t.Errorf("%s with no END: a line after the BEGIN was shown:\n%s", label, shown)
			}
		}
	}
}

// An edit beside a PEM block never echoes it: not in the receipt, not in a
// refusal, whether old_text is shown text, text inside the block, or the
// block's placeholder.
func TestAnEditBesideAPEMEchoesNoneOfIt(t *testing.T) {
	for _, value := range []string{
		pemRunConfig,
		"upstream mirror.example.test\n" + strings.Join(pemLines("RSA PRIVATE KEY", true), "\n") + "\ncache 30\n",
	} {
		provider, _, _ := editProvider(t, value)
		masker := &configMasker{fingerprints: provider.newFingerprints()}
		shown := masker.value("runcfg", value)
		placeholder := shown[strings.Index(shown, "<redacted"):]
		placeholder = placeholder[:strings.Index(placeholder, ">")+1]
		for _, old := range []string{pemLastLine, pemBody[:20], "MFwwDQYJKoZIhvcNAQEBBQ", placeholder, "-----END"} {
			for _, dry := range []bool{true, false} {
				request := editRequest(provider, value, old, "plain.example.test")
				request.DryRun = dry
				receipt, err := provider.EditConfigMapText(context.Background(), request)
				if err == nil {
					t.Errorf("old_text %q inside or for the masked block was edited: %+v", old, receipt)
				}
				carriesNoSecret(t, receipt, err)
				encoded, _ := json.Marshal(receipt)
				for _, hidden := range []string{pemBody[:16], "MFwwDQYJKoZIhvcNAQEBBQ", "BEGIN "} {
					if strings.Contains(err.Error(), hidden) || strings.Contains(string(encoded), hidden) {
						t.Errorf("old_text %q: %q echoed", old, hidden)
					}
				}
			}
		}
	}
}
