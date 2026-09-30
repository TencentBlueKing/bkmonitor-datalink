package contract

import "testing"

func TestResolveOutputWireFormat(t *testing.T) {
	for _, tc := range []struct {
		format   string
		revision int64
		want     string
	}{
		{WireFormatStandardRawEvent, 7, WireFormatStandardRawEvent},
		{WireFormatPythonCompatible, 7, WireFormatPythonCompatible},
		{WireFormatPythonCompatible, 0, WireFormatPythonCompatible},
		{WireFormatTriggerEvent, 7, WireFormatStandardRawEvent},
		{WireFormatTriggerEvent, 0, WireFormatStandardRawEvent},
		{"", 7, WireFormatStandardRawEvent},
		{"", 0, WireFormatPythonCompatible},
		{"unknown", 7, "unknown"},
	} {
		if got := ResolveOutputWireFormat(tc.format, tc.revision); got != tc.want {
			t.Errorf("ResolveOutputWireFormat(%q, %d) = %q, want %q", tc.format, tc.revision, got, tc.want)
		}
	}
}
