package process

import "testing"

func TestGetProcState(t *testing.T) {
	mgr := &procPerfMgr{}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "single letter sleep", input: "S", want: "sleeping"},
		{name: "single letter running", input: "R", want: "running"},
		{name: "single letter blocked", input: "D", want: "idle"},
		{name: "single letter idle", input: "I", want: "idle"},
		{name: "single letter stopped", input: "T", want: "stopped"},
		{name: "single letter traced stop", input: "t", want: "stopped"},
		{name: "single letter zombie", input: "Z", want: "zombie"},
		{name: "semantic sleep", input: "sleep", want: "sleeping"},
		{name: "semantic stop", input: "stop", want: "stopped"},
		{name: "semantic zombie", input: "zombie", want: "zombie"},
		{name: "unknown", input: "x", want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mgr.getProcState(tt.input); got != tt.want {
				t.Fatalf("getProcState(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
