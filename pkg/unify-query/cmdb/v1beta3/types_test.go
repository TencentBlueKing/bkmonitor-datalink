// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package v1beta3

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResourceIdentityRequiresEveryDeclaredField(t *testing.T) {
	labels := map[string]string{"tenant": "acme", "id": "", "region": "east"}
	require.Equal(t, GenerateResourceID("service", labels), generateResourceIdentityKey("service", nil, labels))
	require.NotEmpty(t, generateResourceIdentityKey("service", []string{"id"}, labels))
	key := generateResourceIdentityKey("service", []string{"tenant", "id"}, labels)
	require.NotEmpty(t, key)
	require.Equal(t, key, generateResourceIdentityKey("service", []string{"tenant", "id"}, map[string]string{"id": "", "tenant": "acme", "region": "west"}))
	require.NotEqual(t, key, generateResourceIdentityKey("database", []string{"tenant", "id"}, labels))
	require.Empty(t, generateResourceIdentityKey("service", []string{"tenant", "id"}, map[string]string{"tenant": "acme"}))
	require.Empty(t, generateResourceIdentityKey("service", []string{"tenant", "id"}, map[string]string{"id": ""}))
	require.Empty(t, generateResourceIdentityKey("service", []string{"id"}, nil))
}
