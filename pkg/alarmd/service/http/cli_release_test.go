// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpservice

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/clirelease"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

func packedRelease(t *testing.T, version string) *clirelease.Release {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, clirelease.Placeholder), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err := clirelease.Pack(root, version, func(_ clirelease.Target, dst string) error {
		return os.WriteFile(dst, []byte("#!/bin/sh\n"), 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	return clirelease.LoadFS(os.DirFS(root), version)
}

// A new operator needs the CLI exactly when the deployment cannot otherwise
// be read: before the runtime installs its API handler, and once the public
// surface is restricted to CLI sessions. The distribution routes answer in
// both, and the rest of /api/ keeps its own answer.
func TestTheCLIReleaseIsServedBeforeTheRuntimeAndBehindARestrictedSurface(t *testing.T) {
	release := packedRelease(t, "0.4.4801")
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithRestrictedPublicSurface(), WithCLIRelease(release))
	for _, path := range []string{clirelease.VersionPath, clirelease.ManifestPath, clirelease.InstallPath,
		clirelease.ReleasesPath + "0.4.4801/" + clirelease.ArtifactName("0.4.4801", "linux-x64")} {
		assertStatus(t, server.Handler(), path, http.StatusOK)
	}
	assertStatus(t, server.Handler(), "/api/cli/session", http.StatusServiceUnavailable)

	reached := ""
	server.SetAPI(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) { reached = request.URL.Path }))
	assertStatus(t, server.Handler(), clirelease.VersionPath, http.StatusOK)
	if reached != "" {
		t.Fatalf("the runtime's API handler saw %s", reached)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/cli/session", nil))
	if reached != "/api/cli/session" {
		t.Fatalf("the rest of /api/cli/ no longer reaches the runtime: %q", reached)
	}
}

func TestAServerWithoutAReleaseSaysSo(t *testing.T) {
	server := New(metric.NewRecorder(metric.BuildInfo{}))
	assertStatus(t, server.Handler(), clirelease.InstallPath, http.StatusServiceUnavailable)
}
