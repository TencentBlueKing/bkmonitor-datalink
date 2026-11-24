// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package generate_support_files

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elastic/go-ucfg/yaml"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/basereport/collector"
)

func TestResourceLimitSetDoesNotTrimFollowingNewline(t *testing.T) {
	paths := []string{"write_root_conf_tpl.sh"}

	err := filepath.WalkDir("../../support-files/templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Base(path) == "bkmonitorbeat.conf.tpl" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk support-files templates: %v", err)
	}

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if strings.Contains(string(content), "{%- set resource_limit = resource_limit | default({}) -%}") {
			t.Fatalf("%s trims the newline after resource_limit set, which can render invalid YAML", path)
		}
	}
}

func TestListExtraVarsUseJSONSerialization(t *testing.T) {
	listVars := []string{
		"virtual_iface_whitelist",
		"virtual_iface_blacklist",
		"disk_white_list",
		"disk_black_list",
		"partition_white_list",
		"partition_black_list",
		"mountpoint_white_list",
		"mountpoint_black_list",
		"fs_type_white_list",
		"fs_type_black_list",
		"interface_white_list",
		"interface_black_list",
		"force_report_list",
		"disk_ro_white_list",
		"disk_ro_black_list",
	}

	paths := []string{"write_root_conf_tpl.sh"}
	err := filepath.WalkDir("../../support-files/templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Base(path) == "bkmonitorbeat.conf.tpl" {
			paths = append(paths, path)
		}
		return nil
	})
	require.NoError(t, err)

	for _, path := range paths {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		text := string(content)
		for _, listVar := range listVars {
			expr := "extra_vars." + listVar
			require.Contains(t, text, expr, path)
			for _, line := range strings.Split(text, "\n") {
				if strings.Contains(line, expr) && strings.Contains(line, "}}") {
					require.Contains(t, line, "| tojson }}", "%s: %s", path, listVar)
				}
			}
		}
	}
}

func TestRenderedInterfaceWhiteListMatchesInterfaces(t *testing.T) {
	for _, pattern := range []string{`^eth\\d+$`, `^eth[0-9]+$`} {
		cfg, err := yaml.NewConfig([]byte(`interface_white_list: ["` + pattern + `"]`))
		require.NoError(t, err)

		var netConfig configs.NetConfig
		require.NoError(t, cfg.Unpack(&netConfig))
		compiled := make([]*regexp.Regexp, 0, len(netConfig.InterfaceWhiteListPattern))
		for _, item := range netConfig.InterfaceWhiteListPattern {
			re, err := regexp.Compile(item)
			require.NoError(t, err)
			compiled = append(compiled, re)
		}

		require.True(t, collector.CheckBlackWhiteList("eth0", compiled, nil), pattern)
		require.True(t, collector.CheckBlackWhiteList("eth1", compiled, nil), pattern)
	}

	cfg, err := yaml.NewConfig([]byte(`interface_white_list: []`))
	require.NoError(t, err)
	var netConfig configs.NetConfig
	require.NoError(t, cfg.Unpack(&netConfig))
	require.Empty(t, netConfig.InterfaceWhiteListPattern)
	require.True(t, collector.CheckBlackWhiteList("eth0", nil, nil))
}
