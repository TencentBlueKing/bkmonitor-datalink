// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstanceIdentityValue(t *testing.T) {
	tests := []struct {
		name   string
		source instanceIdentity
		want   string
	}{
		{
			name: "kubernetes pod uid wins over docker and ip",
			source: instanceIdentity{
				podUID: "9bc96f1a-c77d-4a31-86dd-a1f5db176365", containerID: "abcdef123456",
				hostname: "abcdef123456", ip: "192.0.2.36", inKubernetes: true, inDocker: true,
			},
			want: "k8s_9bc96f1a-c77d-4a31-86dd-a1f5db176365",
		},
		{
			name: "old kubernetes chart keeps ip during rollout",
			source: instanceIdentity{
				hostname: "abcdef123456", ip: "192.0.2.36", inKubernetes: true, inDocker: true,
			},
			want: "unify-query@192.0.2.36",
		},
		{
			name: "docker explicit container id",
			source: instanceIdentity{
				containerID: "abcdef1234567890", hostname: "custom-uq", ip: "192.0.2.17", inDocker: true,
			},
			want: "docker_abcdef1234567890",
		},
		{
			name: "docker default hostname is short container id",
			source: instanceIdentity{
				hostname: "abcdef123456", ip: "192.0.2.17", inDocker: true,
			},
			want: "docker_abcdef123456",
		},
		{
			name:   "host uses ip",
			source: instanceIdentity{hostname: "linux-host", ip: "192.0.2.10"},
			want:   "unify-query@192.0.2.10",
		},
		{
			name:   "invalid docker override does not change host identity",
			source: instanceIdentity{containerID: "custom", hostname: "linux-host", ip: "192.0.2.10"},
			want:   "unify-query@192.0.2.10",
		},
		{
			name:   "host without ipv4 uses hostname",
			source: instanceIdentity{hostname: "linux-host"},
			want:   "unify-query@linux-host",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.source.value("unify-query"))
		})
	}
}

func TestDockerCustomHostnameRequiresContainerID(t *testing.T) {
	source := instanceIdentity{hostname: "custom-uq", ip: "192.0.2.17", inDocker: true}
	require.Empty(t, source.value("unify-query"))
}

func TestNewResourceUsesDockerContainerIDOverride(t *testing.T) {
	t.Setenv("UQ_POD_UID", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("UQ_CONTAINER_ID", "abcdef1234567890")

	var instanceID string
	for _, item := range new(Service).newResource().Attributes() {
		if item.Key == "service.instance.id" {
			instanceID = item.Value.AsString()
		}
	}
	require.Equal(t, "docker_abcdef1234567890", instanceID)
}
