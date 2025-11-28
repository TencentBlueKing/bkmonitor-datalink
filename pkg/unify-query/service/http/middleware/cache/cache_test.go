package cache

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestService_isSkipPath(t *testing.T) {
	type shouldPathTestStruct struct {
		name       string
		skipPaths  []string
		input      string
		shouldPass bool
	}
	tests := []shouldPathTestStruct{
		{
			name:       "should pass abs equal",
			skipPaths:  []string{"/a/b"},
			input:      "/a/b",
			shouldPass: true,
		},
		{
			name:       "should pass likely equal",
			skipPaths:  []string{"/a/b*"},
			input:      "/a/b/c/d",
			shouldPass: true,
		},
		{
			name:       "should not pass different",
			skipPaths:  []string{"/a/b"},
			input:      "/a/b/c",
			shouldPass: false,
		},
		{
			name:       "should pass root wildcard",
			skipPaths:  []string{"/*"},
			input:      "/any/path/here",
			shouldPass: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(
				t,
				skipPath(tt.input, tt.skipPaths),
				tt.shouldPass,
			)
		})
	}
}
