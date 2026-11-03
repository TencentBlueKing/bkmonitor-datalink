package uq

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Differential fuzzing covers whitespace, escaped names, nested unknown
// values and duplicate keys. Only JSON-validated objects reach this helper.
func FuzzSharedSeriesFieldsMatchStrictObject(f *testing.F) {
	for _, seed := range []string{
		`{"s":0,"g":["host"],"r":[[1700123000000,12.34]]}`,
		` { "r" : [ [ null , 9007199254740993 ] ] , "g" : [], "s" : 0 } `,
		`{"\u0073":0,"g":["escaped\\\"[}]"],"r":[],"future":{"a":[true,"\\",null]}}`,
		`{"s":0,"s":1,"g":[],"r":[]}`,
		`{"s":0,"\u0073":1,"g":[],"r":[]}`,
		`{"future":{},"\u0066uture":[],"s":0,"g":[],"r":[]}`,
		`{"s":null,"g":null,"r":null}`,
		`{}`, `[]`, `null`, `{"nested":[{"brace":"}"}],"r":[[1,{"object":2}]],"g":[],"s":0}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if !json.Valid([]byte(body)) {
			return
		}
		want, wantErr := wireObject([]byte(body))
		got, gotErr := sharedSeriesFields([]byte(body))
		if (wantErr != nil) != (gotErr != nil) {
			t.Fatalf("strict object acceptance differs for %q: old=%v new=%v", body, wantErr, gotErr)
		}
		if gotErr == nil {
			fields := [3]json.RawMessage{want["s"], want["g"], want["r"]}
			if !reflect.DeepEqual(got, fields) {
				t.Fatalf("encoded values differ for %q: old=%s new=%s", body, fields, got)
			}
		}
	})
}

func TestWireUnsignedRejectsNullAndNonCanonicalIntegers(t *testing.T) {
	for _, sample := range []struct {
		raw  string
		want bool
	}{
		{"0", true}, {"18446744073709551615", true}, {"18446744073709551616", false},
		{"null", false}, {"", false}, {"-0", false}, {"01", false}, {"+1", false},
		{"1e0", false}, {"1.0", false}, {`"1"`, false},
	} {
		var value uint64
		if got := wireUnsigned(json.RawMessage(sample.raw), &value); got != sample.want {
			t.Fatalf("unsigned %q accepted=%t want=%t", sample.raw, got, sample.want)
		}
	}
}
