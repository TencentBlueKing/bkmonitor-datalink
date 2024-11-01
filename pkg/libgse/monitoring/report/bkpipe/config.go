// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkpipe

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"time"
)

// 监控数据上报配置：如果DataID为空，则直接打日志
type config struct {
	BkBizID      int32         `config:"bk_biz_id"`
	DataID       int32         `config:"dataid"`
	TaskDataID   int32         `config:"task_dataid"`
	Period       time.Duration `config:"period"`
	K8sClusterID string        `config:"k8s_cluster_id"`
	K8sNodeName  string        `config:"k8s_node_name"`
	ExtraLabels  []ExtraLabel  `config:"extra_labels"`
}

var defaultConfig = config{
	BkBizID:      2,
	DataID:       0,
	Period:       60 * time.Second,
	K8sClusterID: os.Getenv("MONITOR_K8S_CLUSTER_ID"),
	K8sNodeName:  os.Getenv("MONITOR_K8S_NODE_NAME"),
}

type ExtraLabel struct {
	Type     string `config:"type"`
	Name     string `config:"name"`
	Source   string `config:"source"`
	ValueRef string `config:"value_ref"`
}

func (el ExtraLabel) Load() map[string]string {
	switch el.Type {
	case "env":
		return el.loadFormEnv()
	case "file":
		return el.loadFromFile()
	}
	return nil
}

func (el ExtraLabel) loadFormEnv() map[string]string {
	env := os.Getenv(el.Source)
	if env == "" {
		return nil
	}
	return map[string]string{el.Name: env}
}

func (el ExtraLabel) loadFromFile() map[string]string {
	b, err := os.ReadFile(el.Source)
	if err != nil {
		return nil
	}

	labels := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewBuffer(b))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		v := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		labels[strings.TrimSpace(parts[0])] = v
	}

	for k, v := range labels {
		if k == el.ValueRef {
			return map[string]string{el.Name: v}
		}
	}

	return nil
}
