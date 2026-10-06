package observability

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeErrorTextRedactsURLsAndBoundsLength(t *testing.T) {
	for _, test := range []struct{ name, input, want string }{
		{"plain", "alarmd worker: duplicate completion binding", "alarmd worker: duplicate completion binding"},
		{"http client error", `Post "https://user:secret@example.test/query/ts": dial tcp 10.0.0.1:443: connect: connection refused`, `Post "<url>": dial tcp 10.0.0.1:443: connect: connection refused`},
		{"query string", "fetch http://example.test/path?token=secret failed", "fetch <url> failed"},
		{"two urls", "a https://x.test/1 b redis://h:6379/0 c", "a <url> b <url> c"},
		{"trailing colon", "reserve https://user:secret@example.test/query: rejected", "reserve <url>: rejected"},
		{"trailing period", "see https://example.test/doc.", "see <url>."},
		{"bare separator", "ratio 1://2 kept", "ratio 1://2 kept"},
		{"scheme only prefix", "://nothing", "://nothing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SanitizeErrorText(test.input); got != test.want {
				t.Fatalf("SanitizeErrorText(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
	long := strings.Repeat("x", 600)
	got := SanitizeErrorText(long)
	if len(got) != maxLoggedErrorBytes+len("...") || !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated length=%d", len(got))
	}
	multibyte := strings.Repeat("字", 300)
	got = SanitizeErrorText(multibyte)
	if !utf8.ValidString(got) || len(got) > maxLoggedErrorBytes+len("...") {
		t.Fatalf("multibyte truncation broke a rune: len=%d valid=%t", len(got), utf8.ValidString(got))
	}
	if SanitizeErrorText(strings.Repeat("y", maxLoggedErrorBytes)) != strings.Repeat("y", maxLoggedErrorBytes) {
		t.Fatal("text at the bound was truncated")
	}
}
