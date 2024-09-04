// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"strings"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/spf13/viper"
	"k8s.io/client-go/rest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/target"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	configDryRunPath                   = "operator.dry_run"
	confAPIServerHostPath              = "operator.apiserver_host"
	confKubeConfigPath                 = "operator.kube_config"
	confTLSInsecurePath                = "operator.tls.tls_insecure"
	confTLSCertFilePath                = "operator.tls.tls_cert_file"
	confTLSKeyFilePath                 = "operator.tls.tls_key_file"
	confTLSCAFilePath                  = "operator.tls.tls_ca_file"
	confMonitorNamespacePath           = "operator.monitor_namespace"
	confDenyTargetNamespacesPath       = "operator.deny_target_namespaces"
	confTargetNamespacesPath           = "operator.target_namespaces"
	confEnableServiceMonitorPath       = "operator.enable_service_monitor"
	confEnablePodMonitorPath           = "operator.enable_pod_monitor"
	confEnablePromRulePath             = "operator.enable_prometheus_rule"
	confEnableStatefulSetWorkerPath    = "operator.enable_statefulset_worker"
	confEnableDaemonSetWorkerPath      = "operator.enable_daemonset_worker"
	confEnableEndpointslicePath        = "operator.enable_endpointslice"
	confKubeletNamespacePath           = "operator.kubelet.namespace"
	confKubeletNamePath                = "operator.kubelet.name"
	confKubeletEnablePath              = "operator.kubelet.enable"
	confMaxNodeSecretRatioPath         = "operator.node_secret_ratio"
	confStatefulSetWorkerHpaPath       = "operator.statefulset_worker_hpa"
	confStatefulSetWorkerFactorPath    = "operator.statefulset_worker_factor"
	confStatefulSetReplicasPath        = "operator.statefulset_replicas"
	confStatefulSetMaxReplicasPath     = "operator.statefulset_max_replicas"
	confStatefulSetMatchRulesPath      = "operator.statefulset_match_rules"
	confStatefulSetDispatchTypePath    = "operator.statefulset_dispatch_type"
	confStatefulSetWorkerRegexPath     = "operator.statefulset_worker_regex"
	confMonitorBlacklistMatchRulesPath = "operator.monitor_blacklist_match_rules"
	confHttpPortPath                   = "operator.http.port"
	confPromSdConfigsPath              = "operator.prom_sd_configs"

	// confEnableProbePath                = "operator.enable_probe" // TODO(mando): 待支持
)

const (
	dispatchTypeHash       = "hash"
	dispatchTypeRoundrobin = "roundrobin"
)

// StatefulSetMatchRule statefulset 匹配规则
// 提供一种机制可以通知 operator 将 monitor 资源调度到 statefulset worker 上
// 1) 如果 rule 中 name 为空表示命中所有的 resource
// 2) 如果 rule 中 name 不为空则要求精准匹配
type StatefulSetMatchRule struct {
	Kind      string `mapstructure:"kind"`
	Name      string `mapstructure:"name"`
	Namespace string `mapstructure:"namespace"`
}

// PromSDConfig prometheus 提供的 sdconfigs
// 需要同时指定 namespace 以及 name
type PromSDConfig struct {
	Namespace string `mapstructure:"namespace"`
	Name      string `mapstructure:"name"`
}

// MonitorBlacklistMatchRule monitor 黑名单匹配规则
// 在 monitor namespace 黑名单机制外再提供一种 name 级别的屏蔽机制
// 要求 kind/name/namespace 三者同时不为空 且此配置项优先级最高
type MonitorBlacklistMatchRule struct {
	Kind      string `mapstructure:"kind" json:"kind"`
	Name      string `mapstructure:"name" json:"name"`
	Namespace string `mapstructure:"namespace" json:"namespace"`
}

func (r MonitorBlacklistMatchRule) Validate() bool {
	return r.Kind != "" && r.Namespace != "" && r.Name != ""
}

var (
	// ConfDryRun 是否使用 dryrun 模式 该模式只匹配 不执行真实的调度逻辑
	ConfDryRun bool

	// ConfKubeConfig 连接 kubernetes 使用的 kubeconfig 文件路径
	ConfKubeConfig string

	// ConfAPIServerHost 链接 kubernetes 使用的 API host
	ConfAPIServerHost string

	// ConfTLSConfig 链接 kubernetes 的 TLS 配置
	ConfTLSConfig *rest.TLSClientConfig

	// ConfMonitorNamespace 程序运行所处 namespace
	ConfMonitorNamespace string

	// ConfTargetNamespaces namespace 匹配白名单
	ConfTargetNamespaces []string

	// ConfDenyTargetNamespaces namespace 匹配黑名单
	ConfDenyTargetNamespaces []string

	// ConfEnableServiceMonitor 是否启用 servicemonitor
	ConfEnableServiceMonitor bool

	// ConfEnablePodMonitor 是否启用 podmonitor
	ConfEnablePodMonitor bool

	// ConfEnablePromRule 是否启用 promrules 自监控专用
	ConfEnablePromRule bool

	// ConfEnableStatefulSetWorker 是否启用 statefulset worker 调度
	ConfEnableStatefulSetWorker bool

	// ConfEnableDaemonSetWorker 是否启用 daemonset worker 调度
	ConfEnableDaemonSetWorker bool

	// ConfEnableEndpointslice 是否启用 endpointslice 特性（kubernetes 版本要求 >= 1.22
	ConfEnableEndpointslice bool

	// ConfKubeletNamespace kubelet 组件所在 namespace
	ConfKubeletNamespace string

	// ConfKubeletName kubelet 组件 endpoints 名称
	ConfKubeletName string

	// ConfKubeletEnable 是否启用 kubelet 特性
	ConfKubeletEnable bool

	// ConfMaxNodeSecretRatio 最大支持的 secrets 数量 maxSecrets = node x ratio
	ConfMaxNodeSecretRatio float64

	// ConfStatefulSetWorkerHpa 是否开启 statefulset worker HPA 特性
	ConfStatefulSetWorkerHpa bool

	// ConfStatefulSetWorkerFactor statefulset worker 调度因子 即单 worker 最多支持的 secrets 数量
	ConfStatefulSetWorkerFactor int

	// ConfStatefulSetReplicas statefulset worker 最小副本数
	ConfStatefulSetReplicas int

	// ConfStatefulSetMaxReplicas statefulset worker 最大副本数
	ConfStatefulSetMaxReplicas int

	// ConfStatefulSetMatchRules statefulset worker 匹配规则
	ConfStatefulSetMatchRules []StatefulSetMatchRule

	// ConfStatefulSetDispatchType statefulset worker 调度算法
	ConfStatefulSetDispatchType string

	// ConfStatefulSetWorkerRegex statefulset worker 名称匹配规则 用于锁定具体 worker 索引
	ConfStatefulSetWorkerRegex string

	// ConfMonitorBlacklistMatchRules monitor 黑名单匹配规则
	ConfMonitorBlacklistMatchRules []MonitorBlacklistMatchRule

	// ConfHttpPort http 服务监听端口
	ConfHttpPort int

	// ConfPromSdConfigs promethues sdconfigs secrets 资源
	ConfPromSdConfigs []PromSDConfig
)

// IfRejectServiceMonitor 判断是否拒绝 serviceMonitor
func IfRejectServiceMonitor(monitor *promv1.ServiceMonitor) bool {
	if monitor == nil {
		return false
	}
	for _, rule := range ConfMonitorBlacklistMatchRules {
		if !rule.Validate() {
			continue
		}
		if strings.ToUpper(rule.Kind) == strings.ToUpper(monitor.Kind) && rule.Namespace == monitor.Namespace && rule.Name == monitor.Name {
			return true
		}
	}
	return false
}

// IfRejectPodMonitor 判断是否拒绝 podMonitor
func IfRejectPodMonitor(monitor *promv1.PodMonitor) bool {
	if monitor == nil {
		return false
	}
	for _, rule := range ConfMonitorBlacklistMatchRules {
		if !rule.Validate() {
			continue
		}
		if strings.ToUpper(rule.Kind) == strings.ToUpper(monitor.Kind) && rule.Namespace == monitor.Namespace && rule.Name == monitor.Name {
			return true
		}
	}
	return false
}

func initConfig() {
	viper.SetDefault(confMonitorNamespacePath, "bkmonitor-operator")
	viper.SetDefault(confEnableServiceMonitorPath, true)
	viper.SetDefault(confEnablePodMonitorPath, true)
	viper.SetDefault(confEnableStatefulSetWorkerPath, true)
	viper.SetDefault(confEnableDaemonSetWorkerPath, true)
	viper.SetDefault(confKubeletNamePath, "bkmonitor-operator-kubelet")
	viper.SetDefault(confKubeletNamespacePath, "bkmonitor-operator")
	viper.SetDefault(confKubeletEnablePath, true)
	viper.SetDefault(confMaxNodeSecretRatioPath, 2.0)
	viper.SetDefault(confStatefulSetWorkerHpaPath, true)
	viper.SetDefault(confStatefulSetWorkerFactorPath, defaultStatefulSetWorkerFactor)
	viper.SetDefault(confStatefulSetReplicasPath, 1)
	viper.SetDefault(confStatefulSetMaxReplicasPath, 10)
	viper.SetDefault(confStatefulSetDispatchTypePath, dispatchTypeHash)
	viper.SetDefault(confStatefulSetWorkerRegexPath, "bkmonitor-operator/bkm-statefulset-worker")

	// 同步端口给到 target
	viper.SetDefault(confHttpPortPath, 8080)
	target.ConfServicePort = 8080
}

func updateConfig() {
	ConfDryRun = viper.GetBool(configDryRunPath)
	ConfKubeConfig = viper.GetString(confKubeConfigPath)
	ConfAPIServerHost = viper.GetString(confAPIServerHostPath)
	ConfMonitorNamespace = viper.GetString(confMonitorNamespacePath)
	ConfDenyTargetNamespaces = viper.GetStringSlice(confDenyTargetNamespacesPath)
	ConfTargetNamespaces = viper.GetStringSlice(confTargetNamespacesPath)
	ConfEnableServiceMonitor = viper.GetBool(confEnableServiceMonitorPath)
	ConfEnablePodMonitor = viper.GetBool(confEnablePodMonitorPath)
	ConfEnablePromRule = viper.GetBool(confEnablePromRulePath)
	ConfEnableStatefulSetWorker = viper.GetBool(confEnableStatefulSetWorkerPath)
	ConfEnableDaemonSetWorker = viper.GetBool(confEnableDaemonSetWorkerPath)
	ConfEnableEndpointslice = viper.GetBool(confEnableEndpointslicePath)
	ConfKubeletNamespace = viper.GetString(confKubeletNamespacePath)
	ConfKubeletName = viper.GetString(confKubeletNamePath)
	ConfKubeletEnable = viper.GetBool(confKubeletEnablePath)
	ConfMaxNodeSecretRatio = viper.GetFloat64(confMaxNodeSecretRatioPath)
	ConfTLSConfig = &rest.TLSClientConfig{
		Insecure: viper.GetBool(confTLSInsecurePath),
		CertFile: viper.GetString(confTLSCertFilePath),
		KeyFile:  viper.GetString(confTLSKeyFilePath),
		CAFile:   viper.GetString(confTLSCAFilePath),
	}
	ConfStatefulSetWorkerHpa = viper.GetBool(confStatefulSetWorkerHpaPath)
	ConfStatefulSetWorkerFactor = viper.GetInt(confStatefulSetWorkerFactorPath)
	ConfStatefulSetReplicas = viper.GetInt(confStatefulSetReplicasPath)
	ConfStatefulSetMaxReplicas = viper.GetInt(confStatefulSetMaxReplicasPath)
	ConfStatefulSetDispatchType = viper.GetString(confStatefulSetDispatchTypePath)
	ConfStatefulSetWorkerRegex = viper.GetString(confStatefulSetWorkerRegexPath)

	ConfHttpPort = viper.GetInt(confHttpPortPath)
	target.ConfServicePort = ConfHttpPort

	// reload 时状态需要置空
	ConfStatefulSetMatchRules = []StatefulSetMatchRule{}
	if viper.IsSet(confStatefulSetMatchRulesPath) {
		if err := viper.UnmarshalKey(confStatefulSetMatchRulesPath, &ConfStatefulSetMatchRules); err != nil {
			logger.Errorf("failed to unmarshal ConfStatefulSetMatchRules, err: %v", err)
		}
	}
	ConfMonitorBlacklistMatchRules = []MonitorBlacklistMatchRule{}
	if viper.IsSet(confMonitorBlacklistMatchRulesPath) {
		if err := viper.UnmarshalKey(confMonitorBlacklistMatchRulesPath, &ConfMonitorBlacklistMatchRules); err != nil {
			logger.Errorf("failed to unmarshal ConfMonitorBlacklistMatchRules, err: %v", err)
		}
	}
	ConfPromSdConfigs = []PromSDConfig{}
	if viper.IsSet(confPromSdConfigsPath) {
		if err := viper.UnmarshalKey(confPromSdConfigsPath, &ConfPromSdConfigs); err != nil {
			logger.Errorf("failed to unmarshal ConfPromSdConfigs, err: %v", err)
		}
	}
}

func init() {
	if err := config.EventBus.Subscribe(config.EventConfigPreParse, initConfig); err != nil {
		logger.Errorf("failed to subscribe event %s, err: %v", config.EventConfigPreParse, err)
	}

	if err := config.EventBus.Subscribe(config.EventConfigPostParse, updateConfig); err != nil {
		logger.Errorf("failed to subscribe event %s, err: %v", config.EventConfigPostParse, err)
	}
}
