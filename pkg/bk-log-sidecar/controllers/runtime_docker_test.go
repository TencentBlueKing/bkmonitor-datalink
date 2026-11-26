// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package controllers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
)

func TestDockerAPIVersion(t *testing.T) {
	oldSocket, oldVersion := config.DockerSocket, config.DockerApiVersion
	t.Cleanup(func() { config.DockerSocket, config.DockerApiVersion = oldSocket, oldVersion })
	for _, tc := range []struct {
		name, server, pin, expected, rejection string
		fromEnv                                bool
	}{
		{name: "old daemon", server: "1.38", expected: "1.38"},
		{name: "new daemon", server: "1.40", expected: "1.40"},
		{name: "SDK maximum", server: "1.99", expected: api.DefaultVersion},
		{name: "manual override", server: "1.40", pin: "1.38", expected: "1.38"},
		{name: "environment endpoint", server: "1.38", expected: "1.38", fromEnv: true},
		{name: "no common version", server: "1.99", expected: api.DefaultVersion, rejection: "Minimum supported API version is 1.98"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := "/v" + tc.expected
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/_ping" {
					w.Header().Set("API-Version", tc.server)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case prefix + "/containers/json":
					if tc.rejection != "" {
						w.WriteHeader(http.StatusBadRequest)
						fmt.Fprintf(w, `{"message":"client version %s is too old. %s"}`, tc.expected, tc.rejection)
						return
					}
					fmt.Fprint(w, `[{"Id":"fixture"}]`)
				case prefix + "/events":
					fmt.Fprintln(w, `{"Type":"container","Action":"start","id":"fixture","Actor":{"ID":"fixture"}}`)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				case prefix + "/containers/fixture/json":
					fmt.Fprint(w, `{"Id":"fixture","State":{"Pid":1},"Config":{},"Mounts":[{"Source":"/host/log","Destination":"/log"}]}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			endpoint := "tcp://" + strings.TrimPrefix(server.URL, "http://")
			config.DockerSocket, config.DockerApiVersion = endpoint, tc.pin
			if tc.fromEnv {
				config.DockerSocket = ""
				t.Setenv("DOCKER_HOST", endpoint)
				t.Setenv("DOCKER_API_VERSION", "")
				t.Setenv("DOCKER_CERT_PATH", "")
			}
			runtime, err := NewDockerRuntime()
			require.NoError(t, err)
			defer runtime.(*DockerRuntime).cli.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			entries, err := runtime.Containers(ctx)
			if tc.rejection != "" {
				require.ErrorContains(t, err, "Docker API version incompatible")
				require.ErrorContains(t, err, tc.rejection)
				return
			}
			require.NoError(t, err)
			require.Len(t, entries, 1)
			c, err := runtime.Inspect(ctx, entries[0].ID)
			require.NoError(t, err)
			require.Len(t, c.Mounts, 1)
			require.Equal(t, "/host/log", c.Mounts[0].HostPath)
			require.Equal(t, "/log", c.Mounts[0].ContainerPath)
			events, errs, err := runtime.Subscribe(ctx)
			require.NoError(t, err)
			select {
			case event := <-events:
				require.NotNil(t, event)
				require.Equal(t, "fixture", event.ContainerID)
			case err := <-errs:
				t.Fatalf("event stream failed: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
