package structured

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeDimensions(t *testing.T) {
	type args struct {
		original      []string
		addDimensions []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "test_1",
			args: args{
				original:      []string{"a", "b", "c"},
				addDimensions: []string{"b", "c", "d"},
			},
			want: []string{"a", "b", "c", "d"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, MergeDimensions(tt.args.original, tt.args.addDimensions), "MergeDimensions(%v, %v)", tt.args.original, tt.args.addDimensions)
		})
	}
}
