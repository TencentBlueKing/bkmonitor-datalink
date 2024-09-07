// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"os"

	"gopkg.in/yaml.v2"
	"k8s.io/client-go/rest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/env"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// StatefulSetMatchRule statefulset 匹配规则
// 提供一种机制可以通知 operator 将 monitor 资源调度到 statefulset worker 上
// 1) 如果 rule 中 name 为空表示命中所有的 resource
// 2) 如果 rule 中 name 不为空则要求精准匹配
type StatefulSetMatchRule struct {
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

// PromSDSecret prometheus 提供的 sdconfigs secret
// 需要同时指定 namespace 以及 name
type PromSDSecret struct {
	Namespace string `yaml:"namespace"`
	Name      string `yaml:"name"`
}

// Validate 校验 PromSDSecret 是否合法
func (p PromSDSecret) Validate() bool {
	return p.Namespace != "" && p.Name != ""
}

// MonitorBlacklistMatchRule monitor 黑名单匹配规则
// 在 monitor namespace 黑名单机制外再提供一种 name 级别的屏蔽机制
// 要求 kind/name/namespace 三者同时不为空 且此配置项优先级最高
type MonitorBlacklistMatchRule struct {
	Kind      string `yaml:"kind" json:"kind"`
	Name      string `yaml:"name" json:"name"`
	Namespace string `yaml:"namespace" json:"namespace"`
}

// Validate 校验黑名单列表是否合法
func (r MonitorBlacklistMatchRule) Validate() bool {
	return r.Kind != "" && r.Namespace != "" && r.Name != ""
}

// TLS 与 Kubernetes 通信的 tls 配置
type TLS struct {
	Insecure bool   `yaml:"tls_insecure"`
	CertFile string `yaml:"tls_cert_file"`
	KeyFile  string `yaml:"tls_key_file"`
	CaFile   string `yaml:"tls_ca_file"`
}

// Kubelet 采集配置
type Kubelet struct {
	Enable    bool   `yaml:"enable"`
	Namespace string `yaml:"namespace"`
	Name      string `yaml:"name"`
}

// HTTP http 服务配置
type HTTP struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

func setupHTTP(c *Config) {
	if c.HTTP.Port <= 0 {
		c.HTTP.Port = 8080
	}
}

// Event kubernetes 事件采集配置
type Event struct {
	MaxSpan   string   `yaml:"max_span"`        // 事件最大允许时间跨度
	Interval  string   `yaml:"scrape_interval"` // 事件上报周期
	TailFiles []string `yaml:"scrape_path"`     // 事件监听路径
}

func setupEvent(c *Config) {
	if c.Event.MaxSpan == "" {
		c.Event.MaxSpan = "2h" // 默认事件最大时间跨度为 2h
	}
	if c.Event.Interval == "" {
		c.Event.Interval = "60s" // 默认事件上报周期为 60s
	}
	if len(c.Event.TailFiles) == 0 {
		c.Event.TailFiles = []string{"/var/log/gse/events.log"} // 内置路径
	}
}

// Logger 日志配置
type Logger struct {
	Level string `yaml:"level"`
}

func setupLogger(c *Config) {
	logger.SetOptions(logger.Options{
		Stdout: true,
		Format: "logfmt",
		Level:  c.Logger.Level,
	})
}

// PromSli 自监控配置
type PromSli struct {
	Namespace     string        `yaml:"namespace"`
	SecretName    string        `yaml:"secret_name"`
	ConfigMapName string        `yaml:"configmap_name"`
	Scrape        PromSliScrape `yaml:"prometheus"`
}

// PromSliScrape prometheus 抓取目标配置
type PromSliScrape struct {
	Global    map[string]interface{} `yaml:"global"`
	RuleFiles []string               `yaml:"rule_files"`
	Alerting  map[string]interface{} `yaml:"alerting"`
}

// Config Operator 进程主配置
type Config struct {
	// BkEnv 环境配置信息
	BkEnv string `yaml:"bk_env"`

	// DryRun 是否使用 dryrun 模式 该模式只匹配 不执行真实的调度逻辑
	DryRun bool `yaml:"dry_run"`

	// APIServerHost 连接 kubernetes 使用的 API host
	APIServerHost string `yaml:"apiserver_host"`

	// KubeConfig 连接 kubernetes 使用的 kubeconfig 文件路径
	KubeConfig string `yaml:"kube_config"`

	// DefaultPeriod 默认采集周期
	DefaultPeriod string `yaml:"default_period"`

	// MonitorNamespace 程序所在 namespace
	MonitorNamespace string `yaml:"monitor_namespace"`

	// DenyTargetNamespaces namespace 匹配黑名单
	DenyTargetNamespaces []string `yaml:"deny_target_namespaces"`

	// TargetNamespaces namespace 匹配白名单
	TargetNamespaces []string `yaml:"target_namespaces"`

	// EnableServiceMonitor 是否启用 servicemonitor
	EnableServiceMonitor bool `yaml:"enable_service_monitor"`

	// EnablePodMonitor 是否启用 podmonitor
	EnablePodMonitor bool `yaml:"enable_pod_monitor"`

	// EnablePromRule 是否启用 promrules 自监控专用
	EnablePromRule bool `yaml:"enable_prometheus_rule"`

	// EnableStatefulSetWorker 是否启用 statefulset worker 调度
	EnableStatefulSetWorker bool `yaml:"enable_statefulset_worker"`

	// EnableDaemonSetWorker 是否启用 daemonset worker 调度
	EnableDaemonSetWorker bool `yaml:"enable_daemonset_worker"`

	// EnableEndpointSlice 是否启用 endpointslice 特性（kubernetes 版本要求 >= 1.22
	EnableEndpointSlice bool `yaml:"enable_endpointslice"`

	// NodeSecretRatio 最大支持的 secrets 数量 maxSecrets = node x ratio
	NodeSecretRatio float64 `yaml:"node_secret_ratio"`

	// StatefulSetWorkerHpa 是否开启 statefulset worker HPA 特性
	StatefulSetWorkerHpa bool `yaml:"statefulset_worker_hpa"`

	// StatefulSetWorkerFactor statefulset worker 调度因子 即单 worker 最多支持的 secrets 数量
	StatefulSetWorkerFactor float64 `yaml:"statefulset_worker_factor"`

	// StatefulSetReplicas statefulset worker 最小副本数
	StatefulSetReplicas int `yaml:"statefulset_replicas"`

	// StatefulSetMaxReplicas statefulset worker 最大副本数
	StatefulSetMaxReplicas int `yaml:"statefulset_max_replicas"`

	// StatefulSetDispatchType statefulset worker 调度算法
	StatefulSetDispatchType string `yaml:"statefulset_dispatch_type"`

	// StatefulSetWorkerRegex statefulset worker 名称匹配规则 用于锁定具体 worker 索引
	StatefulSetWorkerRegex string `yaml:"statefulset_worker_regex"`

	// BuiltinLabels 内置 labels 用于补充 bk_ 前缀
	BuiltinLabels []string `yaml:"builtin_labels"`

	// ServiceName operator 注册 service 名称
	ServiceName string `yaml:"service_name"`

	TLS     TLS          `yaml:"tls"`
	HTTP    HTTP         `yaml:"http"`
	Kubelet Kubelet      `yaml:"kubelet"`
	Event   Event        `yaml:"event"`
	Logger  Logger       `yaml:"logger"`
	PromSli PromSli      `yaml:"sli"`
	MetaEnv env.Metadata `yaml:"meta_env"`

	StatefulSetMatchRules      []StatefulSetMatchRule      `yaml:"statefulset_match_rules"`
	MonitorBlacklistMatchRules []MonitorBlacklistMatchRule `yaml:"monitor_blacklist_match_rules"`
	PromSDSecrets              []PromSDSecret              `yaml:"prom_sd_configs"`
}

func setupStatefulSetWorker(c *Config) {
	if c.StatefulSetWorkerRegex == "" {
		c.StatefulSetWorkerRegex = "bkmonitor-operator/bkm-statefulset-worker"
	}
	if c.StatefulSetReplicas <= 0 {
		c.StatefulSetReplicas = 1
	}
	if c.StatefulSetMaxReplicas <= 0 {
		c.StatefulSetMaxReplicas = 10
	}
	if c.StatefulSetWorkerFactor <= 0 {
		c.StatefulSetWorkerFactor = 600
	}
}

// GetTLS 转换 tls 配置为 restclinet tls
func (c *Config) GetTLS() *rest.TLSClientConfig {
	return &rest.TLSClientConfig{
		Insecure: c.TLS.Insecure,
		CertFile: c.TLS.CertFile,
		KeyFile:  c.TLS.KeyFile,
		CAFile:   c.TLS.CaFile,
	}
}

func (c *Config) setup() {
	c.MetaEnv = env.Load()
	funcs := []func(c *Config){
		setupLogger,
		setupEvent,
		setupHTTP,
		setupStatefulSetWorker,
	}

	for _, fn := range funcs {
		fn(c)
	}
	if c.DefaultPeriod == "" {
		c.DefaultPeriod = "60s" // 默认采集周期为 60s
	}
	if c.NodeSecretRatio <= 0 {
		c.NodeSecretRatio = 2.0
	}
}

var gConfig = &Config{}

// G 返回全局加载的 Config
func G() *Config {
	return gConfig
}

// Load 从文件中加载 Config
func Load(p string) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	newConfig := &Config{}
	if err := yaml.Unmarshal(b, newConfig); err != nil {
		return err
	}

	newConfig.setup()
	gConfig = newConfig
	return nil
}
