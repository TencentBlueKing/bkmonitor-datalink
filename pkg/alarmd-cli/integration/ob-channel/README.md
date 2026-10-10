# OB 通道跨组件黑盒

使用真实 CLI 二进制、alarmd 的 cliauth/obchannel/obevidence 和本机临时 Redis、HTTP/HTTPS 服务。需要 Go 1.23+、redis-server、允许本机随机端口监听，以及含 OB channel 的 alarmd 源码。不会访问线上系统或修改系统证书库。

```sh
BLACKBOX_SERVER_REPO=/path/to/bkmonitor-datalink bash integration/ob-channel/run.sh
```

默认 CLI 为本仓；server 在 bkmonitor-datalink 的 pkg/ 下时取同仓，否则取同级 bkmonitor-datalink；BLACKBOX_CLI_REPO 可覆盖。脚本使用临时 modfile，不改被测仓库的依赖或提交状态。结果在本目录 results/run-*/report.json，含两端 commit/dirty、被测二进制 SHA256 和各步骤结果。成功执行后测试 session 已撤销。失败时目录可能留有构造测试凭据，不要整包上传结果目录。

分别覆盖私有 CA HTTPS（14 步）、自签且域名不匹配的 HTTPS + `--insecure-tls`（14 步）、部署指定 HTTP（13 步）：贴码登录、discover/describe、真实源阈值 80 与大结果落盘、缺键完整事实、错型 partial、runtime/Fleet 传输、记录缺失 partial、status/logout、撤销后拒绝。两种 HTTPS 均先验证默认拒绝不可信证书且不发送兑换。检查取证回执中的实际传输模式、stdout ≤20 KiB、真实结果文件 0600，以及授权码/token/admin key/源秘密不泄漏。

入口只转发 URL 前缀，不注入任何凭据。各传输用例验证 alarmd 直接接受部署管理员密钥，GET/POST 授权接口拒绝缺失或错误密钥。runtime/Fleet 的业务内容是构造数据；本测试证明跨组件合同，不能代替部署或告警业务验收。

同一次运行还会执行多实例用例：真实 CLI 经私有 CA HTTPS 访问入口，入口通过 `ControlService.ReadEvidence` 到 Leader，再到 Worker。复用上述登录、Redis、结果落盘和保密检查，覆盖 describe 公布目标参数、指定实例/QueryGroup owner、Worker 独立 Redis 前缀中的阈值 91、完整结果文件保留回答实例/启动身份/build/via、共享 Fleet 查询不路由、重启身份与混版 catalog 不匹配不执行也不回退。临期会话在入口只续期一次，Leader/Worker 不接收 CLI token 或摘要，也不操作 CLI 会话。

多实例目录另有 `routing-report.json`，记录不含凭据的 RPC 跳转和执行/续期计数。三个角色使用真实本机 gRPC 服务、evidenceroute/viewstream 与通道实现；实例注册和活跃租约由只读 fixture 构造，不证明生产 Redis 租约行为（该行为由服务端 ownership 的真实 Redis 测试覆盖）。进程 runtime 内容仍为构造数据。

Slot 用例要求服务端包含 `SlotOperations` 和 UQ `DiagnosticClient`。它从 `object.get` 返回的真实 `next_call` 取得 Slot，再执行 `slot.get` 的 `slot.query` 建议；查询默认经入口 → Leader → 当前 owner 的真实控制 RPC，显式 `replica` 可选择其他仍在注册表中的进程。`slot-report.json` 记录安全的 UQ 请求正文、语义请求头、请求次数、RPC 跳转及各角色会话准入次数。

Slot 用例验证预览零 UQ 请求、原条件重查只请求一次、点值和归一化时间/时序身份、完整尾部解码后的 partial、总点数截断、按时序摘要选择不改变查询正文，以及未知 physical digest、历史合同/请求摘要变化、历史合同缺失时零查询。检查 `kind=requery_now`、当前查询时间、实际回答进程和 owner 租约；结果文件与 RPC 都不能出现 CLI 或 UQ 凭据。冻结合同、已准备物理计划、保留记录、注册表和租约为构造数据，UQ 为本机 wire fixture；历史存储重建由服务端测试覆盖，本用例不证明原 Slot 执行时已看到这些点，也不重放检测或验收告警业务。

人工或 Agent 使用时，先从 `object.get` 的 `next_call` 选择一轮已捕获身份的 Slot，把该建议的 `params` 保存为 `slot-get-input.json`，然后逐步执行：

```sh
alarmd-cli invoke slot.get --env ENV --input @slot-get-input.json
# 从上述完整结果中的 next_call 选择一个 slot.query，
# 将其 params 原样保存为 slot-query-input.json（包含三个服务端摘要）。
alarmd-cli invoke slot.query --env ENV --input @slot-query-input.json
```

以 `meta.result_file` 指向的完整文件读取大结果和下一步建议。`slot.get` 不请求 UQ；`slot.query` 是按保留条件发起的本次查询，可能读到迟到数据。原始输入未被完整保留时不能据此断言历史点值或告警结果；`historical_contract_unavailable` 也不能用当前配置替代。UQ 地址与凭据配置在服务端，CLI 输入只携带服务端返回的查询引用与可选输出限制。退出 3 表示证据不完整，应同时检查 provider completion、截断标记和 limitations。
