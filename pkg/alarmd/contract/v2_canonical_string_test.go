package contract

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type canonicalTestString string

// canonicalStringRoundTrip is the encode, decode and re-encode CanonicalJSONV2
// performs for every other closed value. It is kept here so the string fast
// path is checked against the behaviour it replaces rather than against a
// restatement of itself.
func canonicalStringRoundTrip(t *testing.T, value any) []byte {
	t.Helper()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		t.Fatalf("encode %q: %v", value, err)
	}
	raw := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	var output bytes.Buffer
	roundTrip := json.NewEncoder(&output)
	roundTrip.SetEscapeHTML(false)
	if err := roundTrip.Encode(normalized); err != nil {
		t.Fatalf("re-encode %q: %v", raw, err)
	}
	return restoreJSONLineSeparatorsV2(bytes.TrimSuffix(output.Bytes(), []byte{'\n'}))
}

func canonicalStringCorpus() []string {
	const (
		lineSep  = "\u2028"
		paraSep  = "\u2029"
		replaced = "\ufffd"
	)
	return []string{
		"system",
		"2",
		"9f2a7c1e5b3d8f0a4c6e2b9d7f1a3c5e7b9d1f3a5c7e9b1d3f5a7c9e1b3d5f7a",
		" ",
		"\t\n\r",
		"\"",
		"\\",
		"\\\\",
		"\\u2028",
		"\\\\u2028",
		lineSep,
		paraSep,
		"a" + lineSep + "b",
		"a" + paraSep + "b",
		lineSep + paraSep,
		strings.Repeat(lineSep, 8),
		"line" + lineSep + "sep\\u2028literal",
		"<script>&amp;</script>",
		"</>",
		"\u4e2d\u6587\u7ef4\u5ea6\u503c",
		string([]byte{0x00, 0x01, 0x1f}),
		string([]byte{0x7f}),
		"emoji \U0001F600 tail",
		"nul\x00in\x00side",
		replaced,
		"a" + replaced + "b",
		// Invalid UTF-8: the encoder writes an escape the round trip turns back
		// into a literal replacement character, so these must not take the fast
		// path. They are here to prove they do not.
		string([]byte{0xff, 0xfe, 0xfd}),
		"prefix" + string([]byte{0xc3, 0x28}) + "suffix",
		string([]byte{0xed, 0xa0, 0x80}),
		strings.Repeat("x", 4096),
	}
}

// TestCanonicalJSONStringFastPathMatchesRoundTrip proves the fast path returns
// exactly what the decode and re-encode produced, for plain and named string
// types alike. A difference here would silently move every Runtime State key
// and every identity digest derived from a string.
func TestCanonicalJSONStringFastPathMatchesRoundTrip(t *testing.T) {
	for _, sample := range canonicalStringCorpus() {
		expected := canonicalStringRoundTrip(t, sample)
		actual, err := CanonicalJSONV2(sample)
		if err != nil {
			t.Fatalf("canonical %q: %v", sample, err)
		}
		if !bytes.Equal(expected, actual) {
			t.Fatalf("string %q canonicalized to %q, round trip gives %q", sample, actual, expected)
		}
		named := canonicalTestString(sample)
		namedActual, err := CanonicalJSONV2(named)
		if err != nil {
			t.Fatalf("canonical named %q: %v", sample, err)
		}
		if !bytes.Equal(expected, namedActual) {
			t.Fatalf("named string %q canonicalized to %q, round trip gives %q", sample, namedActual, expected)
		}
	}
}

// TestCanonicalJSONStringFastPathKeepsRejections proves the fast path is
// reached only after the checks that reject a value, so the same inputs still
// fail. The empty string encodes to two quote characters and stays accepted.
func TestCanonicalJSONStringFastPathKeepsRejections(t *testing.T) {
	empty, err := CanonicalJSONV2("")
	if err != nil {
		t.Fatalf("empty string: %v", err)
	}
	if string(empty) != `""` {
		t.Fatalf("empty string canonicalized to %q", empty)
	}
	// A lone surrogate escape must still be refused. It cannot come out of the
	// encoder, so it is offered as a raw fragment, which is not a string type
	// and therefore not on the fast path.
	if _, err := CanonicalJSONV2(json.RawMessage(`"\ud800"`)); err == nil {
		t.Fatal("a lone surrogate escape was accepted")
	}
}

// TestCanonicalClosedTypeVerdicts pins which values take the fast path and
// which keep the strict walk, including the ones that must never be treated as
// a closed string.
func TestCanonicalClosedTypeVerdicts(t *testing.T) {
	type nested struct {
		Name   string            `json:"name"`
		Values []int64           `json:"values"`
		Raw    json.RawMessage   `json:"raw"`
		Number json.Number       `json:"number"`
		Bytes  []byte            `json:"bytes"`
		Inner  *canonicalTestStr `json:"inner"`
	}
	for _, sample := range []struct {
		value  any
		closed bool
	}{
		{"plain", true}, {canonicalTestString("named"), true}, {int64(7), true}, {1.5, true}, {true, true},
		{[]string{"a"}, true}, {[2]int{1, 2}, true}, {struct{ A string }{"a"}, true},
		{json.RawMessage(`{"a":1}`), false}, {[]byte(`{"a":1}`), false}, {json.Number("1"), false},
		{nested{}, false}, {&nested{}, false}, {map[string]string{"a": "b"}, false},
	} {
		if got := canonicalClosedType(reflect.TypeOf(sample.value), nil); got != sample.closed {
			t.Fatalf("closed verdict for %T is %v, want %v", sample.value, got, sample.closed)
		}
	}
}

type canonicalTestStr struct {
	Field string `json:"field"`
}
