// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryFlagsPreserveOptionalBooleanValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    any
		enable func(context.Context) context.Context
		read   func(context.Context) bool
	}{
		{"strict", strictQueryFailuresKey{}, WithStrictQueryFailures, IsStrictQueryFailures},
		{"exact", exactTimeGridKey{}, WithExactTimeGrid, IsExactTimeGrid},
		{"left-open", leftOpenTimeWindowKey{}, WithLeftOpenTimeWindow, IsLeftOpenTimeWindow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.False(t, tc.read(parent))
			require.False(t, tc.read(context.WithValue(parent, tc.key, false)))
			require.True(t, tc.read(context.WithValue(parent, tc.key, true)))
			enabled := tc.enable(context.WithValue(parent, tc.key, false))
			require.True(t, tc.read(enabled))
			child := context.WithValue(enabled, namedOutputScopeContextKey{}, "output")
			require.True(t, tc.read(child))
			require.False(t, tc.read(parent))
			cancel()
			require.ErrorIs(t, child.Err(), context.Canceled)
			require.True(t, tc.read(child))
		})
	}
}
