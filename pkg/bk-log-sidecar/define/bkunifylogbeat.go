// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package define

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
)

// BkunifylogbeatConfig config of Bkunifylogbeat
type BkunifylogbeatConfig struct {
	Local []Local `yaml:"local"`
}

// Marshal yaml marshal local config
func (config *BkunifylogbeatConfig) Marshal() ([]byte, error) {

	locals := make([]map[string]interface{}, 0, len(config.Local))

	for _, local := range config.Local {
		// Step 1: Serialize the Local struct to YAML string
		yamlBytes, err := yaml.Marshal(local)
		if err != nil {
			return nil, err
		}

		// Step 2: Deserialize the YAML string to a map structure
		yamlMap := make(map[string]interface{})
		err = yaml.Unmarshal(yamlBytes, &yamlMap)
		if err != nil {
			return nil, err
		}

		// Step 3: Insert the ExtOptions fields into the map
		for key, rawMessage := range local.ExtOptions {
			var value interface{}
			err = json.Unmarshal(rawMessage.Raw, &value)
			if err != nil {
				return nil, err
			}
			yamlMap[key] = value
		}
		locals = append(locals, yamlMap)
	}

	return yaml.Marshal(map[string]interface{}{
		"local": locals,
	})
}

// DockerJSON docker json config
type DockerJSON struct {
	Stream   string `yaml:"stream"`
	Partial  bool   `yaml:"partial"`
	ForceCRI bool   `yaml:"force_cri_logs"`
	CRIFlags bool   `yaml:"cri_flags"`
}

// Local config
type Local struct {
	DataId           int64                  `yaml:"dataid"`
	Input            string                 `yaml:"input"`
	TailFiles        bool                   `yaml:"tail_files"`
	Path             []string               `yaml:"paths"`
	RemovePathPrefix string                 `yaml:"remove_path_prefix"`
	RootFs           string                 `yaml:"root_fs"`
	Mounts           []Mount                `yaml:"mounts"`
	Multiline        Multiline              `yaml:"multiline,omitempty"`
	ExtMeta          map[string]interface{} `yaml:"ext_meta,omitempty"`
	ExcludeFiles     []string               `yaml:"exclude_files,omitempty"`
	Encoding         string                 `yaml:"encoding,omitempty"`
	Package          bool                   `yaml:"package,omitempty"`
	PackageCount     int                    `yaml:"package_count,omitempty"`
	ScanFrequency    string                 `yaml:"scan_frequency,omitempty"`
	CloseInactive    string                 `yaml:"close_inactive,omitempty"`
	IgnoreOlder      string                 `yaml:"ignore_older,omitempty"`
	CleanInactive    string                 `yaml:"clean_inactive,omitempty"`
	Delimiter        string                 `yaml:"delimiter,omitempty"`
	Filters          []Filter               `yaml:"filters,omitempty"`
	OutputFormat     string                 `yaml:"output_format,omitempty"`

	// for container std out
	DockerJSON *DockerJSON `yaml:"docker-json"`

	ExtOptions map[string]runtime.RawExtension `yaml:"-"`
}

// FromBklogConfig from BkLogConfig to Local
func FromBklogConfig(b *v1alpha1.BkLogConfig) Local {
	local := Local{
		DataId:        b.Spec.DataId,
		Input:         b.Spec.Input,
		Package:       b.Spec.Package,
		PackageCount:  b.Spec.PackageCount,
		ScanFrequency: b.Spec.ScanFrequency,
		CleanInactive: b.Spec.CleanInactive,
		CloseInactive: b.Spec.CloseInactive,
		IgnoreOlder:   b.Spec.IgnoreOlder,
		Path:          append(make([]string, 0, len(b.Spec.Path)), b.Spec.Path...),
		ExcludeFiles:  append(make([]string, 0, len(b.Spec.ExcludeFiles)), b.Spec.ExcludeFiles...),
		Delimiter:     b.Spec.Delimiter,
		Filters:       transferCrdFilterToFilter(b.Spec.Filters),
		ExtOptions:    b.Spec.ExtOptions,
	}
	if len(b.Spec.Multiline.Pattern) > 0 {
		local.Multiline = Multiline{
			Pattern:  b.Spec.Multiline.Pattern,
			MaxLines: b.Spec.Multiline.MaxLines,
			Timeout:  b.Spec.Multiline.Timeout,
			Negate:   true,
			Match:    "after",
		}
	}

	// for bcs config, using old output format
	if b.Spec.IsBcsConfig {
		local.OutputFormat = "v1"
	}
	return local
}

func transferCrdFilterToFilter(filters []v1alpha1.Filter) []Filter {
	resultFilters := make([]Filter, 0)
	for _, filterItem := range filters {
		conditions := make([]Condition, 0)
		for _, conditionItem := range filterItem.Conditions {
			conditions = append(conditions, Condition{Key: conditionItem.Key, Op: conditionItem.Op, Index: conditionItem.Index})
		}
		resultFilters = append(resultFilters, Filter{Conditions: conditions})
	}
	return resultFilters
}

// Multiline Multiline config
type Multiline struct {
	Pattern  string `yaml:"pattern"`
	MaxLines int    `yaml:"max_lines"`
	Timeout  string `yaml:"timeout"`
	Negate   bool   `yaml:"negate"`
	Match    string `yaml:"match"`
}

// Filter is bkunifylogbeat filter rule
type Filter struct {
	Conditions []Condition `yaml:"conditions"`
}

// Condition is bkunifylogbeat filter rule
type Condition struct {
	Index string `yaml:"index"`
	Key   string `yaml:"key"`
	Op    string `yaml:"op"`
}
