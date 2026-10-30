// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package metadata

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackendResponseLimitErrorIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *BackendResponseTooLargeError
		want string
	}{
		{"nil", nil, "backend response exceeds its maximum size"},
		{"empty", &BackendResponseTooLargeError{}, "backend response exceeds its maximum size"},
		{"message", &BackendResponseTooLargeError{Message: "decoded body"}, "backend response exceeds its maximum size: decoded body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.err.Error())
			require.ErrorIs(t, fmt.Errorf("query failed: %w", tc.err), ErrBackendResponseTooLarge)
		})
	}
}

func TestBackendResponseBudgetFollowsDerivedContexts(t *testing.T) {
	base := context.Background()
	require.Zero(t, BackendResponseLimit(base))
	MarkBackendResponseLimitExceeded(base)
	require.False(t, BackendResponseLimitExceeded(base))
	for _, size := range []int64{0, 128} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			parent := WithBackendResponseLimit(base, size)
			child, cancel := context.WithCancel(parent)
			defer cancel()
			require.Equal(t, size, BackendResponseLimit(child))
			require.False(t, BackendResponseLimitExceeded(parent))
			MarkBackendResponseLimitExceeded(child)
			require.True(t, BackendResponseLimitExceeded(parent))
			require.True(t, BackendResponseLimitExceeded(child))
			require.False(t, BackendResponseLimitExceeded(base))
			independent := WithBackendResponseLimit(parent, 256)
			require.Equal(t, int64(256), BackendResponseLimit(independent))
			require.False(t, BackendResponseLimitExceeded(independent))
			cancel()
			require.True(t, BackendResponseLimitExceeded(child))
		})
	}
}
