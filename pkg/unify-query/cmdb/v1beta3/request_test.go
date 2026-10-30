// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package v1beta3

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryRequestNormalizeExplicitLimitPolicy(t *testing.T) {
	oldYolo, oldMax := yoloMode, MaxAllowedHops
	t.Cleanup(func() { yoloMode, MaxAllowedHops = oldYolo, oldMax })
	MaxAllowedHops = 5
	for _, test := range []struct {
		name      string
		yolo      bool
		disabled  bool
		limit     int
		hops      int
		wantLimit int
		wantHops  int
	}{
		{"default", false, false, 0, 1, DefaultLimit, 1},
		{"negative default", false, false, -1, 5, DefaultLimit, 5},
		{"explicit", false, false, 7, 6, 7, 5},
		{"disabled", false, true, 0, 1, 0, 1},
		{"disabled negative", false, true, -1, 1, -1, 1},
		{"disabled explicit", false, true, 7, 1, 7, 1},
		{"yolo", true, false, 0, 6, 0, 6},
		{"yolo negative", true, false, -1, 6, -1, 6},
		{"yolo explicit", true, false, 7, 6, 7, 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			yoloMode = test.yolo
			request := QueryRequest{Limit: test.limit, DisableRootLimit: test.disabled, MaxHops: test.hops}
			request.Normalize()
			require.Equal(t, test.wantLimit, request.Limit)
			require.Equal(t, test.wantHops, request.MaxHops)
		})
	}
}

func TestTimeGraphLimitFallbackInputs(t *testing.T) {
	previous := yoloMode
	t.Cleanup(func() { yoloMode = previous })
	yoloMode = false
	require.Equal(t, 100, effectiveTimeGraphLimit(-1, 100))
	require.Equal(t, 100, effectiveTimeGraphLimit(0, 100))
	require.Equal(t, 7, effectiveTimeGraphLimit(7, 100))
}
