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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/objectsref"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/pprofsnapshot"
)

func writeResponse(w http.ResponseWriter, data interface{}) {
	bs, err := json.Marshal(data)
	if err != nil {
		w.Write([]byte(fmt.Sprintf(`{"msg": "%s"}`, err.Error())))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(bs)
}

type checkNamespace struct {
	DenyNamespaces  []string `json:"deny_namespaces"`
	AllowNamespaces []string `json:"allow_namespaces"`
}

func (c *Operator) checkNamespaceRoute() checkNamespace {
	return checkNamespace{
		AllowNamespaces: ConfTargetNamespaces,
		DenyNamespaces:  ConfDenyTargetNamespaces,
	}
}

// CheckNamespaceRoute 检查 namespace 信息
func (c *Operator) CheckNamespaceRoute(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, c.checkNamespaceRoute())
}

func (c *Operator) checkMonitorBlacklistRoute() []MonitorBlacklistMatchRule {
	return ConfMonitorBlacklistMatchRules
}

// CheckMonitorBlacklistRoute 检查黑名单规则
func (c *Operator) CheckMonitorBlacklistRoute(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, c.checkMonitorBlacklistRoute())
}

type checkDataId struct {
	DataId int               `json:"dataid"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

func (c *Operator) checkDataIdRoute() []checkDataId {
	dataIDs := c.dw.DataIDs()
	ret := make([]checkDataId, 0)
	for _, v := range dataIDs {
		ret = append(ret, checkDataId{
			DataId: v.Spec.DataID,
			Name:   v.Name,
			Labels: v.Labels,
		})
	}
	return ret
}

// CheckScrapeRoute 查看拉取指标信息
func (c *Operator) CheckScrapeRoute(w http.ResponseWriter, _ *http.Request) {
	writeResponse(w, c.scrapeAll())
}

// CheckScrapeNamespaceMonitorRoute 根据命名空间查看拉取指标信息
func (c *Operator) CheckScrapeNamespaceMonitorRoute(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	monitor := vars["monitor"]
	w.Header().Set("Transfer-Encoding", "chunked")

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("failed to use chunked writer"))
		return
	}

	ch := c.scrapeForce(namespace, monitor)
	const batch = 1000
	n := 0
	for line := range ch {
		n++
		fmt.Fprint(w, line+"\n")
		if n == batch {
			flusher.Flush()
			n = 0
		}
	}
	flusher.Flush()
}

// CheckDataIdRoute 检查 dataid 信息
func (c *Operator) CheckDataIdRoute(w http.ResponseWriter, _ *http.Request) {
	writeResponse(w, c.checkDataIdRoute())
}

func (c *Operator) checkActiveDiscoverRoute() []define.MonitorMeta {
	var ret []define.MonitorMeta
	c.discoversMut.Lock()
	for _, dis := range c.discovers {
		ret = append(ret, dis.MonitorMeta())
	}
	c.discoversMut.Unlock()
	return ret
}

func (c *Operator) CheckActiveDiscoverRoute(w http.ResponseWriter, _ *http.Request) {
	writeResponse(w, c.checkActiveDiscoverRoute())
}

func (c *Operator) CheckActiveChildConfigRoute(w http.ResponseWriter, _ *http.Request) {
	writeResponse(w, c.recorder.getActiveConfigFile())
}

func (c *Operator) CheckActiveSharedDiscoveryRoute(w http.ResponseWriter, _ *http.Request) {
	writeResponse(w, discover.GetActiveSharedDiscovery())
}

func (c *Operator) CheckMonitorResourceRoute(w http.ResponseWriter, _ *http.Request) {
	writeResponse(w, c.recorder.getMonitorResources())
}

const (
	formatOperatorVersionMsg = `
[√] check operator version
- Description: bkmonitor-operator 版本信息
%s
`
	formatKubernetesVersionSuccessMsg = `
[√] check kubernetes version
- Description: kubernetes 集群版本为 %s
`
	formatKubernetesVersionFailedMsg = `
[x] check kubernetes version
- Description: 无法正确获取 kubernetes 集群版本
`
	formatClusterInfoSuccessMsg = `
[√] check cluster information
- Description: 集群信息
%s
`
	formatClusterInfoFailedMsg = `
[x] check cluster information
- Description: 无法正确获取集群信息，错误信息 %s
`
	formatCheckDataIDFailedMsg = `
[x] check dataids
- Description: 期待 dataids 数量应大于等于 3 个，目前发现 %d 个
- Suggestion: dataid 是由 metadata 组件注入，请确定接入流程是否规范。同时检查 metadata 日志，确定是否出现异常（必要时携带日志联系开发或者运维同学）
  * operator 从启动到监听 dataids 资源可能存在约 20s 的延迟
  * 监控后台为传统部署，日志路径为 /data/bkee/logs/bkmonitorv3/kernel_metadata.log
  * 监控后台为容器部署，请查看 bkmonitor-alarm-cron-worker pod 的日志
`
	formatCheckDataIDSuccessMsg = `
[√] check dataids
- Description: 期待 dataids 数量应大于等于 3 个，目前发现 %d 个
%s
`
	formatDryRunMsg = `
[√] check dryrun
- Description: %s
`
	formatCheckNamespaceMsg = `
[√] check namespaces
- Description: 监测 namespace 白名单列表 %v，namespace 黑名单列表 %v
- Suggestion: 请检查所需监控资源是否位于监测命名空间列表下，黑名单只在白名单列表为空时生效
  * 如若发现所需命名空间没有在监测列表中，请修改 values.yaml 中的 denyTargetNamespaces 或者 targetNamespaces，并 'helm upgrade' 到集群中
`
	formatCheckNamespaceFailedMsg = `
[x] check namespaces
- Description: 监测 namespace 白名单列表 %v，namespace 黑名单列表 %v
- Suggestion: 黑名单列表只在白名单列表为空时生效
`
	formatCheckMonitorBlacklistMsg = `
[√] check monitor blacklist rules
- Description: monitor name 黑名单匹配规则，此规则优先级最高
%s
`
	formatWorkloadMsg = `
[√] check workload
- Description: 集群各类型工作负载数量如下，最近一次更新时间 %v
%s
`
	formatMonitorEndpointMsg = `
[√] check endpoint
- Description: operator 监听 monitor endpoints 数量
%s
`
	formatScrapeMsg = `
[√] check scrape stats
- Description: 总共发现 %d 个 monitor 资源，抓取数据行数为 %d，采集共出现 %d 次错误，更新时间 %s
- Suggestion: 错误可能由 forwardLocal 导致（可忽略），可过滤 'scrape error' 关键字查看详细错误信息。部分指标会有黑白名单机制，此抓取数据不做任何过滤
* TOP%d 数据量如下，详细情况可访问 /check/scrape 路由。%s
%s
`
	formatListNodeMsg = `
[√] check nodes
- Description: 获取集群节点列表成功，节点数量为 %d，最近一次更新时间 %v
`
	formatHandledSecretFailedMsg = `
[x] check kubernetes secrets handled
- Description: 操作 secrets 资源出现错误
- Suggestion: 请检查 apiserver 是否处于异常状态，最近一次操作时间 %v，考虑重启/删除 ${bkm-operator-pod}
`
	formatHandledSecretSuccessMsg = `
[√] check kubernetes secrets handled
- Description: 操作 secrets 资源未出现错误，最近一次操作时间 %v
`
	formatMonitorResourcesMsg = `
[√] check monitor resources
- Description: 通过 '%s' 关键字匹配到以下监控资源。
* 监测到 ServiceMonitor/PodMonitor/Probe 资源以及对应的采集目标，请检查资源数量是否一致
%s
* 生成的 bkmonitorbeat 采集配置文件
%s
`
	formatCheckNamespaceWithoutKeywordMsg = `
[√] check monitor resources
- Description: 无 'monitor' 请求参数，无资源匹配。
`
	formatCheckHangHandledSuccessMsg = `
[√] check kubernetes event handled
- Description: Kubernetes 监听事件处理无卡住情况，接收事件数 %d，处理事件数 %d
`
	formatCheckHangHandledFailedMsg = `
[x] check kubernetes event handled
- Description: Kubernetes 监听事件处理出现卡住情况，接收事件数 %d，处理事件数 %d，考虑重启/删除 ${bkm-operator-pod}
`
	formatOperatorLogMsg = `
[o] bkmonitor-operator logs
- Description: 使用 'kubectl logs -n ${.Release.Namespace} ${bkm-operator-pod}' 查看是否有 ERROR 信息
- Suggestion: 检查 ERROR 日志是否有明显的报错信息，必要时携带日志联系开发或者运维同学
`
	formatBkmonitorbeatTroubleshootingMsg = `
[o] bkmonitorbeat troubleshooting
- Description: 如若上述检查无发现异常问题，则考虑排查 bkmonitorbeat 本身的采集是否出现异常
- Suggestion: 使用 strace 命令抓取 bkmonitorbeat write syscall 数据
  1）根据上述检查得到采集任务所在节点，并使用 'kubectl get pods -n ${.Release.Namespace} -owide' 确定对应的 bkmonitorbeat pod
  2）使用 'kubectl logs -n ${.Release.Namespace} ${bkm-operator-pod}' 查看是否有 ERROR 信息
  3）使用 'kubectl exec it -n ${.Release.Namespace} ${bkmonitorbeat-pod}' 命令查看 bkmonitorbeat 所在进程 pid
  4）使用 'kubectl exec' 执行 'strace -p ${pid} -s 1024000 -f -e write 2>&1 > /tmp/bkmonitorbeat.strace' 等待一分钟导出 strace 数据
  5）过滤 *.strace 文件查看是否有采集任务指标对应的关键字，判断数据是否有写入到 gse sockets，如若有写到 gse 则说明 bkmonitorbeat 本身没问题，需要排查链路问题
  6）链路排查可按照二进制部署排查思路 kafka -> transfer -> influxdb-proxy -> influxdb（必要时携带日志联系开发或者运维同学）
`
)

// CheckRoute 检查集群健康度 检查项如下
//
// 检查 kubernetes 版本信息
// 检查 bkmonitor-operator 版本信息
// 检查 dataids 是否符合预期
// 检查集群信息
// 检查 dryrun 标识是否打开
// 检查监测命名空间是否符合预期
// 检查黑名单匹配规则
// 检查集群负载情况
// 检查采集指标数据量
// 检查节点列表
// 检查处理 secrets 是否有问题
// 检查操作事件是否有卡住情况
// 检查给定关键字监测资源
func (c *Operator) CheckRoute(w http.ResponseWriter, r *http.Request) {
	buf := &bytes.Buffer{}
	var b []byte

	// 检查 kubernetes 版本信息
	if objectsref.KubernetesServerVersion == "" {
		buf.WriteString(formatKubernetesVersionFailedMsg)
	} else {
		buf.WriteString(fmt.Sprintf(formatKubernetesVersionSuccessMsg, objectsref.KubernetesServerVersion))
	}

	// 检查 bkmonitor-operator 版本信息
	b, _ = json.MarshalIndent(c.buildInfo, "", "  ")
	buf.WriteString(fmt.Sprintf(formatOperatorVersionMsg, string(b)))

	// 检查 dataids 是否符合预期
	dataids := c.checkDataIdRoute()
	n := len(dataids)
	if n < 3 {
		w.Write([]byte(fmt.Sprintf(formatCheckDataIDFailedMsg, n)))
		return
	}
	b, _ = json.MarshalIndent(dataids, "", "  ")
	buf.WriteString(fmt.Sprintf(formatCheckDataIDSuccessMsg, n, string(b)))

	// 检查集群信息
	clusterInfo, err := c.dw.GetClusterInfo()
	if err != nil {
		w.Write([]byte(fmt.Sprintf(formatClusterInfoFailedMsg, err.Error())))
		return
	}
	b, _ = json.MarshalIndent(clusterInfo, "", "  ")
	buf.WriteString(fmt.Sprintf(formatClusterInfoSuccessMsg, string(b)))

	// 检查 dryrun 标识是否打开
	if ConfDryRun {
		buf.WriteString(fmt.Sprintf(formatDryRunMsg, "dryrun 模式，operator 不会调度采集任务"))
	} else {
		buf.WriteString(fmt.Sprintf(formatDryRunMsg, "非 dryrun 模式，operator 正常调度采集任务"))
	}

	// 检查监测命名空间是否符合预期
	namespaces := c.checkNamespaceRoute()
	if len(namespaces.DenyNamespaces) > 0 && len(namespaces.AllowNamespaces) > 0 {
		buf.WriteString(fmt.Sprintf(formatCheckNamespaceFailedMsg, namespaces.AllowNamespaces, namespaces.DenyNamespaces))
	} else {
		buf.WriteString(fmt.Sprintf(formatCheckNamespaceMsg, namespaces.AllowNamespaces, namespaces.DenyNamespaces))
	}

	// 检查黑名单匹配规则
	blacklist := c.checkMonitorBlacklistRoute()
	b, _ = json.MarshalIndent(blacklist, "", "  ")
	buf.WriteString(fmt.Sprintf(formatCheckMonitorBlacklistMsg, string(b)))

	// 检查集群负载情况
	workloadInfo, workloadUpdated := objectsref.GetWorkloadInfo()
	b, _ = json.MarshalIndent(workloadInfo, "", "  ")
	buf.WriteString(fmt.Sprintf(formatWorkloadMsg, workloadUpdated.Format(time.RFC3339), string(b)))

	// 检查 Endpoint 数量
	counts := c.recorder.getMonitorActiveConfigCount()
	b, _ = json.MarshalIndent(counts, "", "  ")
	buf.WriteString(fmt.Sprintf(formatMonitorEndpointMsg, string(b)))

	// 检查采集指标数据量
	onScrape := r.URL.Query().Get("scrape")
	if onScrape == "true" {
		stats := c.scrapeAll()
		n = 5
		if n > stats.MonitorCount {
			n = stats.MonitorCount
		}
		b, _ = json.MarshalIndent(stats.Stats[:n], "", "  ")

		warning := "数据行数未超过 300w 警戒线。"
		if stats.LinesTotal > 3000000 {
			warning = "数据行数已超过 300w 警戒线，请重点关注数据库负载！"
		}
		scrapeUpdated := c.scrapeUpdated.Format(time.RFC3339)
		buf.WriteString(fmt.Sprintf(formatScrapeMsg, stats.MonitorCount, stats.LinesTotal, stats.ErrorsTotal, scrapeUpdated, n, warning, string(b)))
	}

	// 检查节点列表
	nodeCount, nodeUpdated := objectsref.GetClusterNodeInfo()
	buf.WriteString(fmt.Sprintf(formatListNodeMsg, nodeCount, nodeUpdated.Format(time.RFC3339)))

	// 检查处理 secrets 是否有问题
	if c.mm.handledSecretFailed <= 0 || c.mm.handledSecretSuccessTime.After(c.mm.handledSecretFailedTime) {
		buf.WriteString(fmt.Sprintf(formatHandledSecretSuccessMsg, c.mm.handledSecretSuccessTime.Format(time.RFC3339)))
	} else {
		buf.WriteString(fmt.Sprintf(formatHandledSecretFailedMsg, c.mm.handledSecretFailedTime.Format(time.RFC3339)))
	}

	// 检查操作事件是否有卡住情况
	for i := 0; i < 2; i++ {
		if c.mm.handledK8sEvent == c.mm.receivedK8sEvent {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if c.mm.handledK8sEvent != c.mm.receivedK8sEvent {
		buf.WriteString(fmt.Sprintf(formatCheckHangHandledFailedMsg, c.mm.receivedK8sEvent, c.mm.handledK8sEvent))
	} else {
		buf.WriteString(fmt.Sprintf(formatCheckHangHandledSuccessMsg, c.mm.receivedK8sEvent, c.mm.handledK8sEvent))
	}

	// 检查给定关键字监测资源
	monitorKeyword := r.URL.Query().Get("monitor")
	if monitorKeyword != "" {
		var monitorResources []MonitorResourceRecord
		for _, mr := range c.recorder.getMonitorResources() {
			if strings.Contains(mr.Name, monitorKeyword) || strings.Contains(mr.Namespace, monitorKeyword) {
				monitorResources = append(monitorResources, mr)
			}
		}
		monitorResourcesContent, _ := json.MarshalIndent(monitorResources, "", "  ")

		var childConfigs []ConfigFileRecord
		for _, cf := range c.recorder.getActiveConfigFile() {
			if strings.Contains(cf.Service, monitorKeyword) {
				childConfigs = append(childConfigs, cf)
			}
		}
		childConfigsContent, _ := json.MarshalIndent(childConfigs, "", "  ")
		buf.WriteString(fmt.Sprintf(formatMonitorResourcesMsg, monitorKeyword, monitorResourcesContent, childConfigsContent))
	} else {
		buf.WriteString(formatCheckNamespaceWithoutKeywordMsg)
	}

	buf.WriteString(formatOperatorLogMsg)
	buf.WriteString(formatBkmonitorbeatTroubleshootingMsg)
	w.Write(buf.Bytes())
}

func (c *Operator) AdminLoggerRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"msg": "/-/logger route only POST method supported"}`))
		return
	}

	level := r.FormValue("level")
	logger.SetLoggerLevel(level)
	w.Write([]byte(`{"status": "success"}`))
}

func (c *Operator) AdminReloadRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"msg": "/-/reload route only POST method supported"}`))
		return
	}

	timer := time.NewTimer(time.Second * 15)
	select {
	case <-timer.C:
		w.Write([]byte(`{"status": "failed"}`))
		w.WriteHeader(http.StatusInternalServerError)
	case beat.ReloadChan <- true:
		w.Write([]byte(`{"status": "success"}`))
	}
}

func (c *Operator) AdminDispatchRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"msg": "/-/dispatch route only POST method supported"}`))
		return
	}

	discover.Publish()
	w.Write([]byte(`{"status": "success"}`))
}

func (c *Operator) ClusterInfoRoute(w http.ResponseWriter, _ *http.Request) {
	clusterInfo, err := c.dw.GetClusterInfo()
	if err != nil {
		w.Write([]byte(fmt.Sprintf(`{"msg": "%s"}`, err.Error())))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	writeResponse(w, clusterInfo)
}

func (c *Operator) VersionRoute(w http.ResponseWriter, _ *http.Request) {
	writeResponse(w, c.buildInfo)
}

func (c *Operator) WorkloadRoute(w http.ResponseWriter, _ *http.Request) {
	writeResponse(w, c.objectsController.WorkloadsRelabelConfigs())
}

func (c *Operator) WorkloadNodeRoute(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	nodeName := vars["node"]
	writeResponse(w, c.objectsController.WorkloadsRelabelConfigsByNodeName(nodeName))
}

func (c *Operator) RelationMetricsRoute(w http.ResponseWriter, _ *http.Request) {
	var lines []byte
	lines = append(lines, objectsref.RelationToPromFormat(c.objectsController.GetNodeRelations())...)
	lines = append(lines, objectsref.RelationToPromFormat(c.objectsController.GetServieRelations())...)
	lines = append(lines, objectsref.RelationToPromFormat(c.objectsController.GetPodRelations())...)
	lines = append(lines, objectsref.RelationToPromFormat(c.objectsController.GetReplicasetRelations())...)

	w.Write(lines)
}

func (c *Operator) IndexRoute(w http.ResponseWriter, _ *http.Request) {
	content := `
# Admin Routes
--------------
* POST /-/logger
* POST /-/reload
* POST /-/dispatch

# Metadata Routes
-----------------
* GET /metrics
* GET /version
* GET /cluster_info
* GET /workload
* GET /workload/node/{node}
* GET /relation/metrics

# Check Routes
--------------
* GET /check?monitor=${monitor}&scrape=true|false
* GET /check/dataid
* GET /check/scrape
* GET /check/scrape/{namespace}
* GET /check/scrape/{namespace}/{monitor}
* GET /check/namespace
* GET /check/monitor_blacklist
* GET /check/active_discover
* GET /check/active_child_config
* GET /check/active_shared_discovery
* GET /check/monitor_resource

# Profile Routes
----------------
* GET /debug/pprof/snapshot
* GET /debug/pprof/cmdline
* GET /debug/pprof/profile
* GET /debug/pprof/symbol
* GET /debug/pprof/trace
* GET /debug/pprof/{other}
`
	w.Write([]byte(content))
}

func (c *Operator) ListenAndServe() error {
	router := mux.NewRouter()
	router.Handle("/metrics", promhttp.Handler())

	// admin 路由
	router.HandleFunc("/-/logger", c.AdminLoggerRoute)
	router.HandleFunc("/-/reload", c.AdminReloadRoute)
	router.HandleFunc("/-/dispatch", c.AdminDispatchRoute)

	// metadata 路由
	router.HandleFunc("/", c.IndexRoute)
	router.HandleFunc("/version", c.VersionRoute)
	router.HandleFunc("/cluster_info", c.ClusterInfoRoute)
	router.HandleFunc("/workload", c.WorkloadRoute)
	router.HandleFunc("/workload/node/{node}", c.WorkloadNodeRoute)
	router.HandleFunc("/relation/metrics", c.RelationMetricsRoute)

	// check 路由
	router.HandleFunc("/check", c.CheckRoute)
	router.HandleFunc("/check/dataid", c.CheckDataIdRoute)
	router.HandleFunc("/check/scrape", c.CheckScrapeRoute)
	router.HandleFunc("/check/scrape/{namespace}", c.CheckScrapeNamespaceMonitorRoute)
	router.HandleFunc("/check/scrape/{namespace}/{monitor}", c.CheckScrapeNamespaceMonitorRoute)
	router.HandleFunc("/check/namespace", c.CheckNamespaceRoute)
	router.HandleFunc("/check/monitor_blacklist", c.CheckMonitorBlacklistRoute)
	router.HandleFunc("/check/active_discover", c.CheckActiveDiscoverRoute)
	router.HandleFunc("/check/active_child_config", c.CheckActiveChildConfigRoute)
	router.HandleFunc("/check/active_shared_discovery", c.CheckActiveSharedDiscoveryRoute)
	router.HandleFunc("/check/monitor_resource", c.CheckMonitorResourceRoute)

	// debug 路由
	router.HandleFunc("/debug/pprof/snapshot", pprofsnapshot.HandlerFuncFor())
	router.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	router.HandleFunc("/debug/pprof/profile", pprof.Profile)
	router.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	router.HandleFunc("/debug/pprof/trace", pprof.Trace)
	router.HandleFunc("/debug/pprof/{other}", pprof.Index)

	addr := ":8080"
	c.srv = &http.Server{
		Handler:      router,
		Addr:         addr,
		WriteTimeout: 2 * time.Minute,
		ReadTimeout:  2 * time.Minute,
	}
	logger.Infof("Running server at: %v", addr)
	return c.srv.ListenAndServe()
}
