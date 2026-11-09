# 路由刷新共享数据集与测试基准

本基准针对当前 master 的 Redis 查询路由实现设计，不修改生产逻辑。所有子用例共享一次初始化的源表数据；单测只读取源表、设置 Redis 前置状态并断言输出。

## 运行与隔离

从 `pkg/bk-monitor-worker` 目录运行，提供专用 MySQL 测试库的 DSN（需要 CREATE TEMPORARY TABLE 权限）：

```sh
export BMW_ROUTE_FIXTURE_DSN='root@unix(/path/to/test-mysql.sock)/route_test?parseTime=true'
go test -gcflags=all=-l ./internal/metadata/service -run '^TestRouteRefresh$' -count=1
# 同进程重复验证全局状态清理
go test -gcflags=all=-l ./internal/metadata/service -run '^TestRouteRefresh$' -count=5
# 单独验证多租户
go test -gcflags=all=-l ./internal/metadata/service -run '^TestRouteRefresh$/tenant' -v
# 覆盖率
go test -gcflags=all=-l ./internal/metadata/service -run '^TestRouteRefresh$' -coverprofile=/tmp/route-refresh.cover
```

未提供 DSN 时明确 skip，不读取 `bmw_test.yaml`。提供 DSN 后连接、建表或初始化失败均为测试失败。连接使用单连接池，所有表都是 MySQL **连接级临时表**，不会创建、修改、删除同名持久表，连接关闭后临时表自动销毁。Redis 使用 miniredis，不依赖外部 Redis。

真实执行 GORM/MySQL 查询和 Redis Hash/PubSub。MySQL 的 `FIND_IN_SET`、`JSON_CONTAINS`、`JSON_VALID`、租户过滤及分页不做替换。仅替换数据库/Redis/缓存依赖入口，以及专门故障用例的失败点。由于使用项目已有 gomonkey 模式，需禁用内联，不能 `t.Parallel()`。

不要直接运行整个 service 包以代替本命令：其他旧测试仍可能连接配置文件里的持久数据库。

## 文件职责

- `../../../testutil/routefixture/fixture.go`：环境初始化、临时表定义、`seedRouteCoreTables`、`seedRouteFeatureTables` 与 `seedUnrelatedRouteTables`。这是整套共享 mock 源数据的定义。
- `../../route_refresh_test.go`：行为断言，源数据初始化与单测分离。
- 本文件：数据关系、覆盖范围、现状问题及运行说明。

临时表的普通列沿用模型类型，移除索引、主键、自增及注释，规范化默认值；不用于验证生产 schema 迁移或唯一约束。APM 归属关联表与字段别名表显式定义所需列。每个用例重置 Redis 和内存缓存，不重建源表；全量/部分对比与重试场景只保留自身阶段间的输出。

## 数据组合

共 26 张临时表，3 个租户，629 条 RT 记录，其中 501 条属于独立的 batch 租户。

| 主题 | 核心组合 |
| --- | --- |
| 租户 | system/other 同名 `cpu.used` 和 `system` 标签，分别指向不同 VM 集群、vm_rt、别名；batch 租户隔离大批量数据 |
| 空间 | bkcc 自有/授权/空业务、bkci 项目及空项目、bksaas 应用、other 租户空间 |
| 指标链路 | InfluxDB 类型 RT + AccessVMRecord、VM、双存储、无关联、缺失集群、停用/删除 RT |
| 指标形态 | fixed/free、单指标拆分、disable_metric_cutter、普通 metric/dimension、字段白名单 |
| V3/V4 生命周期 | V3 过期但 active 指标仍过滤；V4 过期 active 保留、过期 inactive 过滤、新指标保留；字段分页大小设为 2 |
| DataLabel | 一标签多 RT、多标签、Option 别名去重、纯别名、NULL/空标签、无效 JSON、错误 value_type、孤儿 Option、外租户别名 |
| 日志存储 | ES、Doris、两种配置并存时服从 default_storage、默认存储配置缺失、当前集群缺失、停用 RT |
| 虚拟表 | 当前虚拟表引用 origin，继承物理表 DB、历史分段、字段别名；origin 缺失时跳过 |
| 日志历史 | ES → ES → Doris，多段按时间排序；已删除或集群缺失的分段跳过，不影响有效顶层路由 |
| 日志与指标冲突 | 残缺 Doris RT 同时有 VM 绑定，日志认领后不得回退成指标路由 |
| 字段/选项 | 查询选项白名单、字段别名继承、删除别名、外租户同名别名 |
| 容器 | 独占集群、共享多 namespace、共享空 namespace、非法/缺失资源、SaaS namespace |
| 预计算 | 旧 RecordRule 及 V4 预计算空间分组；部分刷新限定目标和同名规则，支持独立规则及日志认领优先级 |
| VM 短链 | 本空间、全局 all、指定 bkci + project 过滤、自定义 filter_key/filter_value、非法配置回退、停用/删除记录 |
| 日志全局路由 | all 与 bkci 扩展、所属业务不覆盖普通无过滤路由、数字业务/负空间 ID 字符串过滤、非法配置与停用 RT 排除 |
| APM | 全局表只扩展同租户 bkci/bksaas，使用 bk_biz_id_alias；独占应用归属、共享数据源、外租户候选、歧义归属不猜测 |
| 分页 | 501 张日志 RT 跨越 500 表批次边界，验证首尾和输出总数 |
| 应用映射 | 一个应用多个空间、停用映射写空数组 |

## 断言范围

78 个服务子用例覆盖选表、payload、Redis field、租户后缀、存储选型、标签展开、全量/部分结果、未选字段保留、预取合并优先级、发布开关、重复发布、清理和故障传播。

新增 `task/space_partial_refresh_test.go` 直接执行真实预取查询和周期刷新任务，对照三个空间的部分刷新 payload。`http/route_refresh_integration_test.go` 通过正常 HTTP 注册入口复用同一数据集验证三个 API。

## 当前结论

### 已由新版逻辑修复，测试防止回归

- 普通 RT 的字段不再因“单独刷新/与自定义时序同批刷新”而不同。
- 一段式日志表补全后的 `.__default__` key 能被清理白名单保留。
- 空间查询失败返回错误，保留此前 Redis 路由，不写入残缺新结果。
- RT 详情 `isPublish=false` 只写入不发布，重复内容不重复发布。
- DataLabel 已支持逗号标签和 Option 别名，清理保留有效别名。

### 仍复现或需明确的现状

- `details/whitelist_current_fields_lost`：仅刷新白名单 RT 时，`composeTableIdFields` 剔除白名单后列表为空，返回空 map，字段缺失仍存在。后续修复时应修改这一现状断言。
- `failure/label_publish_retry_current_no_republish`：标签 HSET 成功、Publish 失败后，重试因值相同返回成功，不补发。
- `failure/log_write_error_is_tolerated_metric_write_error_returned`：日志批次 Redis 写失败按现有容错语义继续并返回成功，指标写失败返回错误。同步接口设计需明确这一差异。
- 部分详情刷新现已限定指定 RT 与同名 RecordRule。`record_rule/standalone_same_name_override_and_log_ownership` 验证规则独立刷新、同名覆盖与日志认领优先级。

上面是本地基于当前 master 的测试结论，不能沿用旧工作区版本的问题清单。

## 验证与限制

78 个服务子用例、5 个空间查询/全量对照子用例和 HTTP 测试通过。另运行现有日志物理表、历史分段、VM/RecordRule payload 与相关查询计数测试，均通过。

不覆盖 Consul 路由、Python Metadata、尚未实现的 HTTP 刷新服务、真实 Redis 集群故障/性能、分布式同时刷新以及生产数据库索引约束。时间相关数据初始化时相对当前时间设置，避免固定日期导致指标自动过期。

## 部分/全量查询验收

共享数据额外包含各 100 条无关集群、旧/V4 预计算、非全局短链、BCS 集群、RT 与别名。通过 GORM logger 记录实际 SQL 和返回行数，验证输出相同的同时减少加载量。

- 单 `cpu.used` 刷新只查询相关集群，ClusterInfo 返回 1 行；规则查询带 RT 条件并返回 0 行。
- 无依赖的缺失/未绑定表不查询 ClusterInfo；同请求不同批次复用已查及缺失集群 ID。
- 虚拟日志表复用当前、origin、历史集群依赖，ClusterInfo 返回 3 行。
- 501 张日志 RT 分成两批，全量 ClusterInfo 仅查一次；系统租户全量的日志、指标和规则共享同一次集群查询。
- 单空间的预计算 SQL 带空间条件，短链限定“本空间或全局”，加载行数不随其他空间 100 条专属记录增加。
- 单空间 BCS 排除集合按候选 DataID 查询；全量先加载 BCS 集合和前缀 RT，后续空间复用。
- DataLabel 按指定 RT 展开相关标签，再重建完整映射，返回行数限定在命中集合。

运行全部共享数据验收：

```sh
go test -gcflags=all=-l ./http ./internal/metadata/service ./internal/metadata/task \
  -run '^(TestRouteRefresh|TestPartialSpaceRefreshQueries|TestRouteRefreshAPIIntegration|TestPartialRouteRefreshRequiresTargets|TestRefreshResultTablePublishAndExecutionError|TestRefreshSpaceReportsLookupAndRefreshErrors|TestRouteRefreshRejectsBlankTargets)$' -count=1
```

MySQL `EXPLAIN FORMAT=TRADITIONAL` 在无生产索引的临时表上，`FIND_IN_SET` 与 JSON 别名查询均为 `ALL`，本次估算扫描约 670/114 行。估算会随统计信息变化；功能查询只返回相关记录，不代表数据库只扫描这些记录。这份结果用于明确残留扫描成本，不能据此推断线上索引效果。

临时表不出现在 MySQL information_schema，共享 fixture 仅在测试期间模拟已创建表的 `HasTable` 结果，让 V4 规则和短链预取执行真实 SQL。源码没有新增数据库迁移或跨请求依赖缓存。
