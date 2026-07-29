// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podterminatingreporter

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func validReporterOptions() ReporterOptions {
	return ReporterOptions{
		ListenAddress:      ":9110",
		Namespace:          "bkmonitor-operator",
		StateConfigMapName: "pod-terminating-reporter-state",
		RefreshInterval:    time.Minute,
		PageLimit:          200,
		RequestTimeout:     15 * time.Second,
		RecoveryHold:       10 * time.Minute,
		StaleAfter:         3 * time.Minute,
		StateMaxBytes:      HardMaxStateBytes,
	}
}

func TestReporterOptionsValidate(t *testing.T) {
	require.NoError(t, validReporterOptions().Validate())

	tests := map[string]func(*ReporterOptions){
		"listen address":      func(options *ReporterOptions) { options.ListenAddress = "invalid" },
		"namespace":           func(options *ReporterOptions) { options.Namespace = "" },
		"state ConfigMap":     func(options *ReporterOptions) { options.StateConfigMapName = "" },
		"refresh interval":    func(options *ReporterOptions) { options.RefreshInterval = 0 },
		"page limit":          func(options *ReporterOptions) { options.PageLimit = 0 },
		"request timeout":     func(options *ReporterOptions) { options.RequestTimeout = 0 },
		"recovery hold":       func(options *ReporterOptions) { options.RecoveryHold = 0 },
		"stale after":         func(options *ReporterOptions) { options.StaleAfter = 0 },
		"state max zero":      func(options *ReporterOptions) { options.StateMaxBytes = 0 },
		"state max too large": func(options *ReporterOptions) { options.StateMaxBytes = HardMaxStateBytes + 1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			options := validReporterOptions()
			mutate(&options)
			require.Error(t, options.Validate())
		})
	}
}

func TestResolveNamespaceUsesExplicitValueOrServiceAccountFile(t *testing.T) {
	namespace, err := ResolveNamespace("explicit", "/does/not/matter")
	require.NoError(t, err)
	require.Equal(t, "explicit", namespace)

	path := filepath.Join(t.TempDir(), "namespace")
	require.NoError(t, os.WriteFile(path, []byte("from-file\n"), 0o600))
	namespace, err = ResolveNamespace("", path)
	require.NoError(t, err)
	require.Equal(t, "from-file", namespace)

	require.NoError(t, os.WriteFile(path, []byte("\n"), 0o600))
	_, err = ResolveNamespace("", path)
	require.ErrorContains(t, err, "empty")
}

func TestRunOnListenerRefreshesAndShutsDownGracefully(t *testing.T) {
	now := time.Now().UTC()
	pod := deletingPod("default", "example", "node-1", now.Add(-3*time.Hour), 0)
	client := fake.NewSimpleClientset(
		&pod,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "bkmonitor-operator",
				Name:      "pod-terminating-reporter-state",
			},
			Data: map[string]string{StateDataKey: `{"version":2,"active":[],"recovery":[]}`},
		},
	)
	options := validReporterOptions()
	options.RefreshInterval = 10 * time.Millisecond
	options.RequestTimeout = time.Second
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- runOnListener(ctx, client, options, listener)
	}()

	require.Eventually(t, func() bool {
		response, requestErr := http.Get("http://" + listener.Addr().String() + "/readyz")
		if requestErr != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 3*time.Second, 20*time.Millisecond)

	response, err := http.Get("http://" + listener.Addr().String() + "/metrics")
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Contains(t, string(body), `pod_terminating_seconds{namespace="default",node="node-1",pod="example"}`)

	cancel()
	require.NoError(t, <-result)
}

func TestHTTPServerTimeoutsFollowRequestTimeout(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	options := validReporterOptions()
	server := newHTTPServer(state, options)

	require.Equal(t, options.RequestTimeout, server.ReadHeaderTimeout)
	require.Equal(t, options.RequestTimeout+httpWriteTimeoutGrace, server.WriteTimeout)
	require.Equal(t, options.RequestTimeout, server.IdleTimeout)
}
