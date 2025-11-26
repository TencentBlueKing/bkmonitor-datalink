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
	"fmt"
	"os"
	"strings"

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
	Selector  string `yaml:"selector"`
}

// Validate 校验 PromSDSecret 是否合法
func (p PromSDSecret) Validate() bool {
	// 优先使用 name 精准匹配
	if p.Name != "" {
		if p.Namespace == "" {
			return false // 精准匹配不允许空 namespace
		}
		return true
	}

	// 使用 selector 匹配
	if p.Selector == "" {
		return false // 不允许空 selector
	}
	// 空 namespace 则表示匹配所有 namespace 的 secrets
	return true
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

func (k Kubelet) String() string {
	return fmt.Sprintf("%s/%s", k.Namespace, k.Name)
}

func (k Kubelet) Validate() bool {
	return k.Namespace != "" && k.Name != ""
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
	Interval  string   `yaml:"interval"`   // 事件上报周期
	TailFiles []string `yaml:"tail_files"` // 事件监听路径
}

func setupEvent(c *Config) {
	if c.Event.Interval == "" {
		c.Event.Interval = "60s" // 默认事件上报周期为 60s
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

// VCluster 配置，bklogconfig 使用中
type VCluster struct {
	PodNameAnnotationKey      string `yaml:"pod_name_annotation_key"`
	PodUidAnnotationKey       string `yaml:"pod_uid_annotation_key"`
	PodNamespaceAnnotationKey string `yaml:"pod_namespace_annotation_key"`
	WorkloadNameAnnotationKey string `yaml:"workload_name_annotation_key"`
	WorkloadTypeAnnotationKey string `yaml:"workload_type_annotation_key"`
	LabelsAnnotationKey       string `yaml:"labels_annotation_key"`
	LabelKey                  string `yaml:"label_key"`
	ManagedAnnotationKey      string `yaml:"managed_annotation_key"`
}

type TimeSync struct {
	Enabled       bool   `yaml:"enabled"`
	NtpdPath      string `yaml:"ntpd_path"`
	ChronyAddress string `yaml:"chrony_address"`
	QueryTimeout  string `yaml:"query_timeout"`
}

// QCloudMonitor 腾讯云监控采集配置
type QCloudMonitor struct {
	// Enabled 是否启用
	Enabled bool `yaml:"enabled"`

	// Private 是否为内部模式
	Private bool `yaml:"private"`

	// TargetNamespaces namespace 匹配白名单
	TargetNamespaces []string `yaml:"target_namespaces"`

	// DenyTargetNamespaces namespace 匹配黑名单
	DenyTargetNamespaces []string `yaml:"deny_target_namespaces"`
}

// ProcessMonitor 进程监控采集配置
type ProcessMonitor struct {
	// Enabled 是否启用
	Enabled bool `yaml:"enabled"`

	// TargetNamespaces namespace 匹配白名单
	TargetNamespaces []string `yaml:"target_namespaces"`

	// DenyTargetNamespaces namespace 匹配黑名单
	DenyTargetNamespaces []string `yaml:"deny_target_namespaces"`
}

// Config Operator 进程主配置
type Config struct {
	// BkEnv 环境配置信息
	BkEnv string `yaml:"bk_env"`

	// LogBkEnv bklogconfig 环境配置信息
	LogBkEnv string `yaml:"log_bk_env"`

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

	// EnableStatefulSetWorker 是否启用 statefulset worker 调度
	EnableStatefulSetWorker bool `yaml:"enable_statefulset_worker"`

	// EnableDaemonSetWorker 是否启用 daemonset worker 调度
	EnableDaemonSetWorker bool `yaml:"enable_daemonset_worker"`

	// DaemonSetWorkerIgnoreNodeLabels 部分 nodes 不允许被调度到 daemonset 时指定
	DaemonSetWorkerIgnoreNodeLabels map[string]string `yaml:"daemonset_worker_ignore_node_labels"`

	// EnableEndpointSlice 是否启用 endpointslice 特性（kubernetes 版本要求 >= 1.22)
	EnableEndpointSlice bool `yaml:"enable_endpointslice"`

	// DispatchInterval 调度周期（单位秒）
	DispatchInterval int64 `yaml:"dispatch_interval"`

	// StatefulSetWorkerHpa 是否开启 statefulset worker HPA 特性
	StatefulSetWorkerHpa bool `yaml:"statefulset_worker_hpa"`

	// StatefulSetWorkerFactor statefulset worker 调度因子 即单 worker 最多支持的 secrets 数量
	StatefulSetWorkerFactor float64 `yaml:"statefulset_worker_factor"`

	// StatefulSetWorkerScaleMaxRetry statefulset worker 调度最大重试次数
	StatefulSetWorkerScaleMaxRetry int `yaml:"statefulset_worker_scale_max_retry"`

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

	TLS         TLS          `yaml:"tls"`
	HTTP        HTTP         `yaml:"http"`
	Kubelet     Kubelet      `yaml:"kubelet"`
	Event       Event        `yaml:"event"`
	Logger      Logger       `yaml:"logger"`
	MetaEnv     env.Metadata `yaml:"meta_env"`
	PromSDKinds PromSDKinds  `yaml:"prom_sd_kinds"`

	StatefulSetMatchRules      []StatefulSetMatchRule      `yaml:"statefulset_match_rules"`
	MonitorBlacklistMatchRules []MonitorBlacklistMatchRule `yaml:"monitor_blacklist_match_rules"`
	PromSDSecrets              []PromSDSecret              `yaml:"prom_sd_configs"`

	VCluster       VCluster       `yaml:"vcluster"`
	PolarisAddress []string       `yaml:"polaris_address"`
	TimeSync       TimeSync       `yaml:"timesync"`
	QCloudMonitor  QCloudMonitor  `yaml:"qcloudmonitor"`
	ProcessMonitor ProcessMonitor `yaml:"processmonitor"`
}

type PromSDKinds []string

func (psk PromSDKinds) Allow(s string) bool {
	if len(psk) == 0 {
		return false
	}
	if len(psk) == 1 && psk[0] == "*" {
		return true
	}

	kinds := make(map[string]struct{})
	for _, kind := range psk {
		kinds[strings.ToLower(kind)] = struct{}{}
	}

	_, ok := kinds[strings.ToLower(s)]
	return ok
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

func setupVCluster(c *Config) {
	if c.VCluster.PodNameAnnotationKey == "" {
		c.VCluster.PodNameAnnotationKey = "vcluster.loft.sh/name"
	}
	if c.VCluster.PodUidAnnotationKey == "" {
		c.VCluster.PodUidAnnotationKey = "vcluster.loft.sh/uid"
	}
	if c.VCluster.PodNamespaceAnnotationKey == "" {
		c.VCluster.PodNamespaceAnnotationKey = "vcluster.loft.sh/namespace"
	}
	if c.VCluster.WorkloadNameAnnotationKey == "" {
		c.VCluster.WorkloadNameAnnotationKey = "vcluster.loft.sh/owner-set-name"
	}
	if c.VCluster.WorkloadTypeAnnotationKey == "" {
		c.VCluster.WorkloadTypeAnnotationKey = "vcluster.loft.sh/owner-set-kind"
	}
	if c.VCluster.LabelsAnnotationKey == "" {
		c.VCluster.LabelsAnnotationKey = "vcluster.loft.sh/labels"
	}
	if c.VCluster.LabelKey == "" {
		c.VCluster.LabelKey = "vcluster.loft.sh/managed-by"
	}
	if c.VCluster.ManagedAnnotationKey == "" {
		c.VCluster.ManagedAnnotationKey = "vcluster.loft.sh/managed-annotations"
	}
}

func setupTimeSync(c *Config) {
	if c.TimeSync.QueryTimeout == "" {
		c.TimeSync.QueryTimeout = "10s"
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
		setupVCluster,
		setupTimeSync,
	}

	for _, fn := range funcs {
		fn(c)
	}
	if c.DefaultPeriod == "" {
		c.DefaultPeriod = "60s" // 默认采集周期为 60s
	}
	if c.DispatchInterval <= 0 {
		c.DispatchInterval = 30 // 默认调度周期为 30s
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
