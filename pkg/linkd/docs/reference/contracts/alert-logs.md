# AlertLog 批量追加

`POST /api/v1/alert-logs` 使用既有内部 HTTP JWT。请求为 `{"logs": [AlertLog, ...]}`，每条含 log_id、bk_tenant_id、alert_id、operator_kind、operation_kind、params、created_time。租户随每条日志保存，调用者为已认证的内部服务；不从用户名推导租户。

复用 Repository.AppendAlertLogs 的批次、字节预算及不可变身份合同。HTTP 200 的 results 与输入逐项对应，每项含 log_id、created、error（成功为 null，失败含 code/message）。相同完整事实重放成功且 created=false；同身份不同事实为 identity_conflict。invalid_argument 表示该项不满足既有存储合同，unavailable 表示该项追加失败。批次无法得到可信逐项结果时返回 HTTP 400（invalid_argument）或 503（unavailable）。HTTP 请求沿用存储默认的 16 MiB 传输预算；请求本身 JSON 错误返回 400，未配置存储返回 503。

成功是存储追加完成，不等待 ES search refresh，也不是队列接收。失败可与成功并存，调用者保存原 ID、created_time 和完整事实重试；不得为成功项重新生成身份。Linkd 负责 ES _id 编码、桶路由与重复核对。

operation_kind 保留原生 13 类，仅扩展 KAC 当前 18 类持久写入值：not_dispatched、dispatched、response、pending_execute、approval、auto_executing、auto_executing_failure、auto_executing_retry、auto_executing_success、auto_ordering、auto_ordering_failure、auto_ordering_finish、manual_closed、auto_closed、system_closed、source_closed、restored、notify。received/enrich 只用于 KAC 派生展示；pending_merge、convergence、merged_into、merged、shielded、claim、auto_ordering_success、feedback 仅保留旧数据展示，不注册为新追加类型。AST 试运行也不注册。

KAC 事实放在 params.content_key/content_params，按需保存准确快照、操作与恢复引用；不存 content/content_format。Linkd 不执行 KAC 模板渲染或通知业务。原生策略日志布局另见 [策略事实合同](policy-flow-records.md)。
