// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package v1beta3

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryRequestNormalizeExplicitLimitPolicy(t *testing.T) {
	oldMax := MaxAllowedHops
	t.Cleanup(func() { MaxAllowedHops = oldMax })
	MaxAllowedHops = 6
	for _, test := range []struct {
		name      string
		disabled  bool
		limit     int
		hops      int
		wantLimit int
		wantHops  int
	}{
		{"default", false, 0, 1, DefaultLimit, 1},
		{"negative default", false, -1, 5, DefaultLimit, 5},
		{"explicit", false, 7, 7, 7, 6},
		{"disabled", true, 0, 1, 0, 1},
		{"disabled negative", true, -1, 1, -1, 1},
		{"disabled explicit", true, 7, 1, 7, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := QueryRequest{Limit: test.limit, DisableRootLimit: test.disabled, MaxHops: test.hops}
			request.Normalize()
			require.Equal(t, test.wantLimit, request.Limit)
			require.Equal(t, test.wantHops, request.MaxHops)
		})
	}
}

func TestTimeGraphLimitFallbackInputs(t *testing.T) {
	require.Equal(t, 100, effectiveTimeGraphLimit(-1, 100))
	require.Equal(t, 100, effectiveTimeGraphLimit(0, 100))
	require.Equal(t, 7, effectiveTimeGraphLimit(7, 100))
}
