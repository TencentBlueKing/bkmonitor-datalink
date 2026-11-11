// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package v1beta3

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryRequestNormalizeHopPolicy(t *testing.T) {
	oldMax := MaxAllowedHops
	t.Cleanup(func() { MaxAllowedHops = oldMax })
	MaxAllowedHops = 6
	for _, test := range []struct {
		name     string
		hops     int
		wantHops int
	}{
		{"default", 0, DefaultMaxHops},
		{"negative default", -1, DefaultMaxHops},
		{"explicit", 5, 5},
		{"clamped", 7, 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := QueryRequest{MaxHops: test.hops}
			request.Normalize()
			require.Equal(t, test.wantHops, request.MaxHops)
		})
	}
}

func TestTimeGraphLimitFallbackInputs(t *testing.T) {
	require.Equal(t, 100, effectiveTimeGraphLimit(-1, 100))
	require.Equal(t, 100, effectiveTimeGraphLimit(0, 100))
	require.Equal(t, 7, effectiveTimeGraphLimit(7, 100))
}
