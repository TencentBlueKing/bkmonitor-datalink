// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package models

// CollectConfig 是 core_v1alpha1_collect 中可用于定位告警对象的只读投影。
// Reader 校验数据库可空的租户、模型和实例字段后，才返回该非空投影。
type CollectConfig struct {
	// UID 是声明式配置主键，不是告警维度中的采集任务 ID。
	UID string
	// BKTenantID 是该配置的租户作用域。
	BKTenantID string
	// BKCollectTaskID 来自 status.bk_collect_task_id；保留字符串身份和前导零。
	BKCollectTaskID string
	// BKObjectCode 是被监控对象的模型代码。
	BKObjectCode string
	// BKInstID 是被监控对象实例，不能用采集执行主机 final_inst_id 替换。
	BKInstID int64
	// FinalBizID 是采集下发目标所属业务，可作为服务实例查询上下文。
	FinalBizID *int64
	// IsRemoteCollect 来自 spec.remote_collect_info.is_remote_collect；
	// 该值只表明采集执行目标可能是关联主机，资源身份仍使用 BKInstID。
	IsRemoteCollect bool
}

// ResourceTopology 是关联主机解析得到的业务、集群和模块投影。
type ResourceTopology struct {
	BKBizID      int64
	BKBizName    string
	BKSetID      int64
	BKSetName    string
	BKModuleID   int64
	BKModuleName string
}

// UptimeProtocol 是拨测任务决定目标展示字段的协议。
type UptimeProtocol string

const (
	UptimeProtocolTCP  UptimeProtocol = "TCP"
	UptimeProtocolUDP  UptimeProtocol = "UDP"
	UptimeProtocolHTTP UptimeProtocol = "HTTP"
	UptimeProtocolICMP UptimeProtocol = "ICMP"
)

// Valid 判断协议是否属于拨测任务当前支持的集合。
func (p UptimeProtocol) Valid() bool {
	return p == UptimeProtocolTCP || p == UptimeProtocolUDP || p == UptimeProtocolHTTP || p == UptimeProtocolICMP
}

// UptimeTask 是 home_application_uptimechecktask 中用于资源展示的只读投影。
type UptimeTask struct {
	// ID 是数据库主键，也是资源 model_inst_id。
	ID int64
	// TaskID 是蓝鲸拨测任务 ID，告警维度 task_id 使用该身份查询记录。
	TaskID int64
	// Name 是拨测任务名称。
	Name string
	// Protocol 决定展示 HTTP、TCP/UDP 或其他协议的目标维度。
	Protocol UptimeProtocol
	// BKBizID 是资源所属业务，不覆盖告警来源业务。
	BKBizID int64
	// BKTenantID 是记录所属租户。
	BKTenantID string
}

// UptimeNode 是 home_application_uptimechecknode 中用于维度展示的只读投影。
type UptimeNode struct {
	// ID 是数据库主键。
	ID int64
	// Name 是拨测节点名称。
	Name string
	// PlatID 与 IP 共同组成告警 node_id 使用的稳定查询身份。
	PlatID int64
	IP     string
	// BKTenantID 是记录所属租户。
	BKTenantID string
}
