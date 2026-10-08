// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"context"
	"fmt"
	"time"

	"linkd/internal/domain"
	"linkd/internal/projection"
	"linkd/internal/store"
)

// ProjectionAlertReader 提供含归档合并水位的实时读取，不能使用活动缓存或搜索快照代替。
type ProjectionAlertReader interface {
	GetAlertCurrent(context.Context, string, string) (store.StoredAlert, error)
}

// ProjectionProofReader 按持久身份实时读取原投影任务，必须返回独立快照并传播取消。
type ProjectionProofReader interface {
	Get(context.Context, string, string) (projection.StoredTask, error)
}

// ProjectionGate 核对已绑定目标的 ACK 及对应持久可见回执，不创建绑定或迁移来源目标。
// 同一来源 ID/Release/目标 ID 必须代表不可变路由；来源适配器不得把旧发布解析到最新目的端。
type ProjectionGate struct {
	alerts ProjectionAlertReader
	proofs ProjectionProofReader
}

// NewProjectionGate 注入当前业务仓储和同部署投影仓储；构造不读取状态或启动任务。
func NewProjectionGate(alerts ProjectionAlertReader, proofs ProjectionProofReader) (*ProjectionGate, error) {
	if alerts == nil || proofs == nil {
		return nil, ErrInvalid
	}
	return &ProjectionGate{alerts: alerts, proofs: proofs}, nil
}

// Check 在五秒总预算内最多读取两次 Alert 和一次投影证明；无写入或接收端 I/O。
// 调用者须持有 Service 与投影发送共用的 Alert/目标租约。ACK 尚未到达或检查期间业务变化返回
// ErrBusy；错作用域/错发布/非法证明返回 ErrInvalidReceipt；仓储和取消错误原样保留错误链。
// 二次读取缩小业务并发窗口，但不锁住之后的业务 CAS；接收端仍必须按版本拒绝旧动作复活终态。
func (g *ProjectionGate) Check(ctx context.Context, task Task) (projection.Receipt, error) {
	if ctx == nil || task.Validate() != nil {
		return projection.Receipt{}, ErrInvalid
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	before, ref, err := g.readAlert(call, task)
	if err != nil {
		return projection.Receipt{}, err
	}
	if ref.SyncedRevision < task.Request.Revision || before.Status.Terminal() && ref.SyncedRevision < before.Revision {
		return projection.Receipt{}, ErrBusy
	}
	id, err := projection.TaskID(task.Request.TenantID, task.Request.AlertID, task.Request.TargetID, ref.SyncedRevision)
	if err != nil {
		return projection.Receipt{}, ErrInvalidReceipt
	}
	row, err := g.proofs.Get(call, task.Request.TenantID, id)
	if call.Err() != nil {
		return projection.Receipt{}, call.Err()
	}
	if err != nil {
		return projection.Receipt{}, fmt.Errorf("read action projection proof: %w", err)
	}
	proof := row.Task
	if row.Version == "" || proof.Validate() != nil || proof.ID != id || proof.SourceID != task.SourceID || proof.SourceVersion != task.SourceVersion ||
		proof.Request.TenantID != task.Request.TenantID || proof.Request.AlertID != task.Request.AlertID || proof.Request.TargetID != task.Request.TargetID ||
		proof.Request.Revision != ref.SyncedRevision || proof.Progress.Receipt == nil {
		return projection.Receipt{}, ErrInvalidReceipt
	}
	receipt := *proof.Progress.Receipt
	if ValidateProjection(task.Request, receipt) != nil || proof.Request.Revision == task.Request.Revision && proof.Request.ContentHash != task.Request.ContentHash {
		return projection.Receipt{}, ErrInvalidReceipt
	}
	// ACK/待输出元数据不进入业务摘要；相同 revision 的证明必须与仓储中的真实业务内容一致。
	if proof.Request.Revision == before.Revision || receipt.AppliedRevision == before.Revision {
		current, err := projection.BuildRequest(before, task.Request.TargetID)
		if err != nil || proof.Request.Revision == before.Revision && proof.Request.ContentHash != current.ContentHash ||
			receipt.AppliedRevision == before.Revision && receipt.ValidateFor(current) != nil {
			return projection.Receipt{}, ErrInvalidReceipt
		}
	}
	after, finalRef, err := g.readAlert(call, task)
	if err != nil {
		return projection.Receipt{}, err
	}
	if after.Revision != before.Revision || after.Status != before.Status || finalRef.RequiredRevision != ref.RequiredRevision || finalRef.SyncedRevision != ref.SyncedRevision {
		return projection.Receipt{}, ErrBusy
	}
	return receipt, nil
}

func (g *ProjectionGate) readAlert(ctx context.Context, task Task) (domain.Alert, domain.ProjectionTargetState, error) {
	if err := ctx.Err(); err != nil {
		return domain.Alert{}, domain.ProjectionTargetState{}, err
	}
	row, err := g.alerts.GetAlertCurrent(ctx, task.Request.TenantID, task.Request.AlertID)
	if ctx.Err() != nil {
		return domain.Alert{}, domain.ProjectionTargetState{}, ctx.Err()
	}
	if err != nil {
		return domain.Alert{}, domain.ProjectionTargetState{}, fmt.Errorf("read action projection alert: %w", err)
	}
	a := row.Alert
	ref, exists := a.Projection.Targets[task.Request.TargetID]
	if row.Version.IsZero() || a.Validate() != nil || a.BKTenantID != task.Request.TenantID || a.AlertID != task.Request.AlertID || a.EventSourceID != task.SourceID ||
		a.Revision < task.Request.Revision || !exists || ref.SourceVersion != task.SourceVersion || ref.RequiredRevision != a.Revision {
		return domain.Alert{}, domain.ProjectionTargetState{}, ErrInvalidReceipt
	}
	return a, ref, nil
}
