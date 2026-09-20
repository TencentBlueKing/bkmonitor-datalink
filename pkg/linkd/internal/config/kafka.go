// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "fmt"

// resolveKafkaTLSPaths 将 YAML 中的 Kafka TLS 相对文件路径固定到主配置文件目录。
// 解析后的绝对路径属于最终生效配置，因此 config print 会如实展示它们。
func (c *Config) resolveKafkaTLSPaths(baseDir string) error {
	for index := range c.EventSources {
		resolved, err := c.EventSources[index].Storage.Kafka.Security.ResolvePaths(baseDir)
		if err != nil {
			return fmt.Errorf("event_sources[%d].storage.kafka.security.%w", index, err)
		}
		c.EventSources[index].Storage.Kafka.Security = resolved
	}
	if c.Lifecycle == nil || c.Lifecycle.Output.Kafka == nil {
		return nil
	}
	resolved, err := c.Lifecycle.Output.Kafka.Security.ResolvePaths(baseDir)
	if err != nil {
		return fmt.Errorf("lifecycle.output.kafka.security.%w", err)
	}
	c.Lifecycle.Output.Kafka.Security = resolved
	return nil
}
