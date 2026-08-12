// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestRawBatchConnectionKeyUsesEffectiveConnectionAndAuthContext(t *testing.T) {
	metadata.InitMetadata()
	leftContext := metadata.InitHashID(context.Background())
	metadata.SetUser(leftContext, &metadata.User{
		Key:      "source:user",
		SpaceUID: "space-one",
		TenantID: "tenant-one",
	})
	rightContext := metadata.InitHashID(context.Background())
	metadata.SetUser(rightContext, &metadata.User{
		Key:      "source:user",
		SpaceUID: "space-one",
		TenantID: "tenant-one",
	})

	left, err := NewInstance(leftContext, &InstanceOption{
		Connect: Connect{
			Address:  "http://es.example",
			UserName: "reader",
			Password: "password",
		},
		Headers: map[string]string{
			"X-Second": "two",
			"X-First":  "one",
		},
		Timeout:     3 * time.Second,
		HealthCheck: false,
	})
	require.NoError(t, err)
	right, err := NewInstance(rightContext, &InstanceOption{
		Connect: Connect{
			Address:  "http://es.example",
			UserName: "reader",
			Password: "password",
		},
		Headers: map[string]string{
			"X-First":  "one",
			"X-Second": "two",
		},
		Timeout:     3 * time.Second,
		HealthCheck: false,
	})
	require.NoError(t, err)

	assert.Equal(t, left.RawBatchConnectionKey(leftContext), right.RawBatchConnectionKey(rightContext))

	differentContext := metadata.InitHashID(context.Background())
	metadata.SetUser(differentContext, &metadata.User{
		Key:      "source:user",
		SpaceUID: "space-two",
		TenantID: "tenant-one",
	})
	assert.NotEqual(t, left.RawBatchConnectionKey(leftContext), left.RawBatchConnectionKey(differentContext))

	differentPassword, err := NewInstance(leftContext, &InstanceOption{
		Connect: Connect{
			Address:  "http://es.example",
			UserName: "reader",
			Password: "different",
		},
		Headers: map[string]string{
			"X-First":  "one",
			"X-Second": "two",
		},
		Timeout: 3 * time.Second,
	})
	require.NoError(t, err)
	assert.NotEqual(t, left.RawBatchConnectionKey(leftContext), differentPassword.RawBatchConnectionKey(leftContext))

	differentTimeout, err := NewInstance(leftContext, &InstanceOption{
		Connect: Connect{
			Address:  "http://es.example",
			UserName: "reader",
			Password: "password",
		},
		Headers: map[string]string{
			"X-First":  "one",
			"X-Second": "two",
		},
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	assert.NotEqual(t, left.RawBatchConnectionKey(leftContext), differentTimeout.RawBatchConnectionKey(leftContext))
}
