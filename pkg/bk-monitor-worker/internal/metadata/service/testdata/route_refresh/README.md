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

- `../../route_refresh_fixture_test.go`：环境初始化、临时表定义、`seedRouteCoreTables` 与 `seedRouteFeatureTables`。这是整套共享 mock 源数据的定义。
- `../../route_refresh_test.go`：行为断言，源数据初始化与单测分离。
- 本文件：数据关系、覆盖范围、现状问题及运行说明。

临时表的普通列沿用模型类型，移除索引、主键、自增及注释，规范化默认值；不用于验证生产 schema 迁移或唯一约束。APM 归属关联表与字段别名表显式定义所需列。每个用例重置 Redis 和内存缓存，不重建源表；全量/部分对比与重试场景只保留自身阶段间的输出。

## 数据组合

共 26 张临时表，3 个租户，528 条 RT 记录，其中 501 条属于独立的 batch 租户。

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
| 预计算 | 旧 RecordRule 及 V4 预计算空间分组；普通指标部分刷新附带租户预计算详情 |
| VM 短链 | 本空间、全局 all、指定 bkci + project 过滤、自定义 filter_key/filter_value、非法配置回退、停用/删除记录 |
| 日志全局路由 | all 与 bkci 扩展、所属业务不覆盖普通无过滤路由、数字业务/负空间 ID 字符串过滤、非法配置与停用 RT 排除 |
| APM | 全局表只扩展同租户 bkci/bksaas，使用 bk_biz_id_alias；独占应用归属、共享数据源、外租户候选、歧义归属不猜测 |
| 分页 | 501 张日志 RT 跨越 500 表批次边界，验证首尾和输出总数 |
| 应用映射 | 一个应用多个空间、停用映射写空数组 |

## 断言范围

69 个子用例覆盖选表、payload、Redis field、租户后缀、存储选型、标签展开、全量/部分结果、未选字段保留、预取合并优先级、发布开关、重复发布、清理和故障传播。

预取测试从共享源表读取记录后执行生产的 `Compose*BySpace`，再验证传给 `PushSpaceTableIds` 的预取数据优先于普通路由。这里没有复制整套调度器，也不声称覆盖周期任务的并发、调度及所有前置查询。

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
- `record_rule/partial_metric_refresh_includes_tenant_rules`：普通指标部分刷新会附带当前租户全部旧版 RecordRule 详情，不能假定只写入传入的 RT。

上面是本地基于当前 master 的测试结论，不能沿用旧工作区版本的问题清单。

## 验证与限制

69 个子用例通过。覆盖率：`spaceredis.go` 73.9%、`result_table_detail.go` 86.6%、`apm_application_route.go` 93.0%。这是逐文件语句覆盖率，不是全系统覆盖率。

不覆盖 Consul 路由、Python Metadata、尚未实现的 HTTP 刷新服务、真实 Redis 集群故障/性能、分布式同时刷新以及生产数据库索引约束。时间相关数据初始化时相对当前时间设置，避免固定日期导致指标自动过期。
