// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/taskdispatch"
)

func newEventSourceCommand(options *commandOptions) *cobra.Command {
	root := &cobra.Command{Use: "event-source", Short: "管理持久化事件来源"}
	var file string
	command := &cobra.Command{Use: "import", Short: "显式通过控制面 API 增加或更新 YAML 来源；不删除未列出项", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, e := loadConfig(cmd, options)
		if e != nil {
			return e
		}
		if file == "" {
			return fmt.Errorf("--file is required")
		}
		input, e := config.Load(file, config.Overrides{})
		if e != nil {
			return e
		}
		d := cfg.Dispatch.WithDefaults()
		client := taskdispatch.Client{URL: d.URL, Token: d.APIToken}
		for _, spec := range input.EventSources {
			var current eventsource.Record
			path := "/api/v1/event-sources/" + url.PathEscape(spec.EventSourceID)
			e = client.Call(cmd.Context(), http.MethodGet, path, nil, &current)
			if e != nil && e.Error() != "control plane HTTP 404" {
				return e
			}
			var result eventsource.Record
			e = client.Call(cmd.Context(), http.MethodPut, path, taskdispatch.Mutation{Expected: current.Revision, Spec: spec}, &result)
			if e != nil {
				return e
			}
			if e = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"id": result.ID, "published": result.Published}); e != nil {
				return e
			}
		}
		return nil
	}}
	command.Flags().StringVar(&file, "file", "", "包含 event_sources 的 YAML 路径")
	root.AddCommand(command)
	return root
}
