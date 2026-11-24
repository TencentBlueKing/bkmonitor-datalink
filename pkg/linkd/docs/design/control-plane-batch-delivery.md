# 控制面 KAC 批量输出

当前实现集中在 `internal/kaccompat`，默认参数与字段所有权以[投影契约](../reference/contracts/kac-alert-projection-v1.md)为准。

每个租户/Alert 固定通道串行，同批重复投影通知合并，读取最新 Alert。原动作独立保留，不能因为合并状态写入而丢掉动作。
普通投影成功后进入独立内存发送缓冲，集中 refresh 再并发调用 KAC；投影线程不等待动作接口。
主动关闭通过同一通道优先执行 `refresh=true`。停止信号不取消正在执行的请求，运行器等待完成；尚未执行的消息保留在原 Stream。

旧投影/动作任务库、快照、CAS、水位、补偿扫描、Receipt 和管理恢复接口已删除。
资源配置统一放在 `plugins.kac.delivery`，不再使用 `control_plane.delivery`。
业务 Lifecycle CAS、Event 计划与审计逻辑保留。

[动作与重启协议](../reference/contracts/kac-action-delivery-v3.md) · [本地验收](../research/2026-10-14-kac-output-v3-validation.md)
