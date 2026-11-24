# 配置驱动事件生成器

状态：2026-10-21 已实现并完成本地验证；测试环境仅独立更新 eventgen，控制面与 Admin 新能力尚未部署。验证范围见[验收记录](../research/benchmarks/2026-10-21-eventgen.md)。

eventgen 是测试环境可选工具，不是 Linkd 的默认依赖。`dispatch.eventgen_enabled` 默认关闭管理存储与后台初始化；Helm `eventgen.enabled` 默认关闭生成进程。显式启用时存储初始化独立重试，生成器离线不改变主链路健康。

独立 eventgen 通过控制面保存的租户任务配置生成 Kafka 消息。Admin 的 Linkd 模块管理数据表、模板、场景与运行。配置包含 datasets、templates、scenarios、limits；运行使用独立 run_id 和进程 session_id。仅配置和运行摘要持久化，进程重启不恢复活动池、不自动开跑。

数据表按整行随机、加权或遍历选择，可通过前一个绑定的字段关联筛选。绑定及 instance 模板在告警创建时求值，事件模板逐事件求值。模板使用 text/template，json 函数负责类型与转义，随机源由 seed 固定。

新增告警速率与生命周期发送间隔独立。阶段包含 duration、every、parameters、template、next；next 可以循环或按权重选择。阶段边界只发送新阶段事件。高级别恢复加低级别触发沿用 Linkd 创建新 Alert 的行为。

速率更新直接生效；其他配置先编译，再停止新增、排空发送、用旧身份收尾，最后切换。Standard 收尾发送 __ALL__ closed，自定义 JSON 使用 cleanup_template。收尾失败保留可重试的待关闭记录，禁止报告成功切换。暂停冻结逻辑时钟，继续不追赶补发。发送预算产生背压，不静默丢消息。Kafka ACK 只证明发送，实际告警结果通过 Admin 联查。

管理 API 位于 /api/v1/eventgen；进程使用独立 Worker Token 访问 /internal/eventgen，默认每 3 秒回报并读取指令。所有任务、运行、表数据与查询均按租户隔离；运行命令绑定当前进程会话，旧启动指令不能在新进程重放。

验收包括可控时钟、模板与数据绑定、热加载、收尾失败、暂停取消、race、ES/MySQL 与 Kafka 集成，以及 1000/5000/10000 EPS 各 10 分钟真实环境测试。未执行的环境验收应单独记录，不以单元测试替代。
