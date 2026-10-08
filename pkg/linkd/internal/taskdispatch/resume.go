package taskdispatch

import (
	"context"
	"time"

	"linkd/internal/eventsource"
)

// ResumeTask 仅解除已停止的确切失败代次；不确认消息，不修改 Release 或事件。
// 来源角色须完成停止握手，才能重新分配新代次并接管退休 consumer 的 PEL。
// 相同代次的重复请求幂等；后续代次的失败不能被旧请求解除。
func (c *Controller) ResumeTask(ctx context.Context, source, id string, version, epoch int64) error {
	if source == "" || id == "" || version <= 0 || epoch <= 0 {
		return eventsource.ErrConflict
	}
	return c.update(ctx, func(state *State) error {
		return resumeTask(state, source, id, version, epoch)
	})
}

func resumeTask(state *State, source, id string, version, epoch int64) error {
	task, ok := state.Tasks[id]
	if !ok || task.Source != source {
		return eventsource.ErrNotFound
	}
	if task.Version != version || task.Epoch != epoch || task.Phase != "stopped" {
		return eventsource.ErrConflict
	}
	for _, other := range state.Tasks {
		if other.Source == source && other.Role == task.Role && other.Phase != "stopped" {
			return eventsource.ErrConflict
		}
	}
	task.Blocked = false
	task.RetryAfter = time.Time{}
	state.Tasks[id] = task
	return nil
}
