# 故障排查指南

本文档提供 Unify-Query 项目常见问题的排查方法和解决方案。

## 目录

1. [查询问题](#1-查询问题)
2. [配置问题](#2-配置问题)
3. [连接问题](#3-连接问题)
4. [性能问题](#4-性能问题)
5. [数据问题](#5-数据问题)
6. [日志分析](#6-日志分析)
7. [监控指标](#7-监控指标)

---

## 1. 查询问题

### 1.1 查询返回空结果

**症状**：查询请求成功，但返回的数据为空。

**可能原因**：
1. 查询时间范围不正确
2. 表不存在或表名错误
3. 字段名不存在
4. 过滤条件过于严格
5. 数据未写入存储

**排查步骤**：

1. **检查查询参数**
   ```bash
   # 查看请求日志，确认查询参数
   curl -X POST http://localhost:10205/query/ts \
     -H "Content-Type: application/json" \
     -d '{
       "space_uid": "your_space",
       "query_list": [...],
       "start_time": "1629810830",
       "end_time": "1629811070"
     }'
   ```

2. **检查表是否存在**
   ```bash
   # 查询表信息
   curl -X POST http://localhost:10205/query/ts/info/field_keys \
     -H "Content-Type: application/json" \
     -d '{"space_uid": "your_space", "table_id": "your_table"}'
   ```

3. **检查数据是否存在**
   - 直接查询存储后端，确认数据是否存在
   - 检查数据的时间戳是否在查询范围内

4. **检查过滤条件**
   - 确认过滤条件是否正确
   - 尝试放宽过滤条件

**解决方案**：
- 调整查询时间范围
- 确认表名和字段名正确
- 检查数据写入情况
- 调整过滤条件

### 1.2 查询超时

**症状**：查询请求超时，返回 504 或超时错误。

**可能原因**：
1. 查询时间范围过大
2. 查询数据量过大
3. 存储后端响应慢
4. 网络问题

**排查步骤**：

1. **检查查询时间范围**
   ```go
   // 检查 start_time 和 end_time 的差值
   duration := endTime - startTime
   // 如果超过 24 小时，可能需要优化查询
   ```

2. **检查查询复杂度**
   - 查看 PromQL 语句的复杂度
   - 检查是否有大量的聚合操作
   - 检查是否有大量的标签匹配

3. **检查存储后端性能**
   ```bash
   # 检查 InfluxDB 性能
   curl http://localhost:8086/debug/vars | jq '.queryExecutor'
   ```

4. **检查网络延迟**
   ```bash
   # 测试到存储后端的网络延迟
   ping storage-host
   ```

**解决方案**：
- 缩小查询时间范围
- 优化查询语句，减少数据量
- 增加查询超时时间（配置文件中）
- 优化存储后端性能
- 使用降采样功能

### 1.3 查询语法错误

**症状**：返回语法错误，例如 "parse error"。

**可能原因**：
1. PromQL 语法错误
2. 字段名或标签名错误
3. 函数参数错误

**排查步骤**：

1. **检查 PromQL 语法**
   ```bash
   # 使用 PromQL 校验接口
   curl -X POST http://localhost:10205/check/query/ts \
     -H "Content-Type: application/json" \
     -d '{"query": "your_promql"}'
   ```

2. **检查字段和标签**
   ```bash
   # 查询可用的字段和标签
   curl -X POST http://localhost:10205/query/ts/info/field_keys \
     -H "Content-Type: application/json" \
     -d '{"space_uid": "your_space", "table_id": "your_table"}'
   ```

3. **查看错误详情**
   - 检查日志中的详细错误信息
   - 确认错误位置和原因

**解决方案**：
- 修正 PromQL 语法
- 使用正确的字段名和标签名
- 检查函数参数是否正确

### 1.4 查询结果不正确

**症状**：查询返回结果，但数据不正确。

**可能原因**：
1. 聚合函数使用错误
2. 时间对齐问题
3. 数据精度问题
4. 时区问题

**排查步骤**：

1. **检查聚合函数**
   - 确认聚合函数的使用是否正确
   - 检查 `by` 或 `without` 子句

2. **检查时间对齐**
   - 确认 `step` 参数设置是否正确
   - 检查时间戳对齐情况

3. **检查数据精度**
   - 确认数据精度是否符合预期
   - 检查是否有数据丢失

4. **检查时区**
   - 确认时区设置是否正确
   - 检查时间戳转换是否正确

**解决方案**：
- 修正聚合函数的使用
- 调整时间对齐参数
- 检查数据精度设置
- 确认时区配置

---

## 2. 配置问题

### 2.1 配置不生效

**症状**：修改配置文件后，配置不生效。

**可能原因**：
1. 配置文件路径错误
2. 配置文件格式错误
3. 配置项名称错误
4. 需要重启服务

**排查步骤**：

1. **检查配置文件路径**
   ```bash
   # 确认配置文件路径
   ls -la unify-query.yaml
   ```

2. **检查配置文件格式**
   ```bash
   # 验证 YAML 格式
   yamllint unify-query.yaml
   ```

3. **检查配置项名称**
   - 查看配置文档，确认配置项名称
   - 检查是否有拼写错误

4. **检查配置加载**
   - 查看启动日志，确认配置是否加载
   - 使用配置热重载功能

**解决方案**：
- 确认配置文件路径正确
- 修正配置文件格式
- 使用正确的配置项名称
- 重启服务或使用热重载

### 2.2 Consul 配置问题

**症状**：无法从 Consul 加载配置。

**可能原因**：
1. Consul 连接失败
2. 配置路径错误
3. 配置格式错误
4. 权限问题

**排查步骤**：

1. **检查 Consul 连接**
   ```bash
   # 测试 Consul 连接
   curl http://localhost:8500/v1/status/leader
   ```

2. **检查配置路径**
   ```bash
   # 查看 Consul 中的配置
   consul kv get -recurse bkmonitorv3/unify-query/
   ```

3. **检查配置格式**
   ```bash
   # 查看配置内容
   consul kv get bkmonitorv3/unify-query/data/storage/8
   ```

4. **检查权限**
   - 确认服务有权限访问 Consul
   - 检查 ACL 配置

**解决方案**：
- 修复 Consul 连接问题
- 使用正确的配置路径
- 修正配置格式
- 配置正确的权限

---

## 3. 连接问题

### 3.1 无法连接存储后端

**症状**：查询失败，错误信息包含 "connection refused" 或 "timeout"。

**可能原因**：
1. 存储后端未启动
2. 网络问题
3. 配置地址错误
4. 防火墙阻止

**排查步骤**：

1. **检查存储后端状态**
   ```bash
   # 检查 InfluxDB
   curl http://localhost:8086/ping
   
   # 检查 Redis
   redis-cli ping
   ```

2. **检查网络连接**
   ```bash
   # 测试网络连接
   telnet storage-host 8086
   ```

3. **检查配置地址**
   - 查看配置文件中的地址配置
   - 确认地址和端口正确

4. **检查防火墙**
   ```bash
   # 检查防火墙规则
   iptables -L
   ```

**解决方案**：
- 启动存储后端服务
- 修复网络问题
- 修正配置地址
- 配置防火墙规则

### 3.2 Redis 连接问题

**症状**：无法从 Redis 获取元数据。

**可能原因**：
1. Redis 未启动
2. Redis 配置错误
3. 密码错误
4. 数据库选择错误

**排查步骤**：

1. **检查 Redis 状态**
   ```bash
   redis-cli ping
   ```

2. **检查 Redis 配置**
   ```yaml
   redis:
     host: 127.0.0.1
     port: 6379
     password: ""
     database: 0
   ```

3. **测试 Redis 连接**
   ```bash
   redis-cli -h 127.0.0.1 -p 6379 ping
   ```

4. **检查数据是否存在**
   ```bash
   redis-cli keys "bkmonitorv3:spaces:*"
   ```

**解决方案**：
- 启动 Redis 服务
- 修正 Redis 配置
- 使用正确的密码
- 选择正确的数据库

---

## 4. 性能问题

### 4.1 查询性能慢

**症状**：查询响应时间过长。

**可能原因**：
1. 查询时间范围过大
2. 数据量过大
3. 存储后端性能问题
4. 网络延迟

**排查步骤**：

1. **分析查询性能**
   ```bash
   # 使用 pprof 分析性能
   go tool pprof http://localhost:6060/debug/pprof/profile
   ```

2. **检查查询时间范围**
   - 确认查询时间范围是否合理
   - 考虑使用降采样

3. **检查数据量**
   - 查看存储后端的数据量
   - 检查是否有数据膨胀

4. **检查存储后端性能**
   ```bash
   # 检查 InfluxDB 性能指标
   curl http://localhost:8086/debug/vars | jq '.queryExecutor'
   ```

**解决方案**：
- 缩小查询时间范围
- 使用降采样功能
- 优化查询语句
- 优化存储后端性能
- 增加缓存

### 4.2 内存使用过高

**症状**：服务内存使用持续增长。

**可能原因**：
1. 缓存过大
2. 查询结果未释放
3. 内存泄漏

**排查步骤**：

1. **检查内存使用**
   ```bash
   # 查看内存使用情况
   top -p $(pgrep unify-query)
   ```

2. **检查缓存大小**
   - 查看缓存配置
   - 检查缓存使用情况

3. **使用内存分析工具**
   ```bash
   # 使用 pprof 分析内存
   go tool pprof http://localhost:6060/debug/pprof/heap
   ```

**解决方案**：
- 调整缓存大小限制
- 优化查询结果处理
- 修复内存泄漏
- 增加内存限制

---

## 5. 数据问题

### 5.1 数据不一致

**症状**：查询结果与预期不符，数据不一致。

**可能原因**：
1. 数据写入延迟
2. 缓存过期
3. 时区问题
4. 数据精度问题

**排查步骤**：

1. **检查数据写入时间**
   - 确认数据是否已写入
   - 检查数据时间戳

2. **检查缓存**
   - 清除缓存后重新查询
   - 检查缓存过期时间

3. **检查时区**
   - 确认时区设置
   - 检查时间戳转换

4. **直接查询存储后端**
   - 绕过缓存直接查询
   - 对比结果差异

**解决方案**：
- 等待数据写入完成
- 清除缓存
- 修正时区配置
- 检查数据精度

### 5.2 数据丢失

**症状**：部分数据查询不到。

**可能原因**：
1. 数据未写入
2. 数据过期被删除
3. 查询条件过滤了数据
4. 存储后端故障

**排查步骤**：

1. **检查数据写入**
   - 确认数据是否成功写入
   - 检查写入日志

2. **检查数据保留策略**
   - 查看数据保留时间
   - 确认数据是否过期

3. **检查查询条件**
   - 放宽查询条件
   - 确认过滤条件是否正确

4. **检查存储后端**
   - 确认存储后端是否正常
   - 检查数据是否完整

**解决方案**：
- 重新写入数据
- 调整数据保留策略
- 修正查询条件
- 修复存储后端

---

## 6. 日志分析

### 6.1 日志级别

日志级别从低到高：
- `DEBUG`：调试信息
- `INFO`：一般信息
- `WARN`：警告信息
- `ERROR`：错误信息

### 6.2 关键日志

#### 查询日志

```log
[INFO] query executed: promql=cpu_usage, duration=100ms
[WARN] query slow: promql=cpu_usage, duration=5s
[ERROR] query failed: promql=cpu_usage, error=timeout
```

#### 连接日志

```log
[INFO] connected to storage: type=influxdb, address=127.0.0.1:8086
[ERROR] failed to connect: type=influxdb, address=127.0.0.1:8086, error=connection refused
```

#### 配置日志

```log
[INFO] config loaded: path=unify-query.yaml
[WARN] config reloaded: signal=SIGUSR1
```

### 6.3 日志分析工具

```bash
# 查看错误日志
grep ERROR unify-query.log

# 查看慢查询
grep "query slow" unify-query.log

# 统计错误数量
grep ERROR unify-query.log | wc -l
```

---

## 7. 监控指标

### 7.1 关键指标

#### 查询指标

- `query_total`：查询总数
- `query_duration_seconds`：查询耗时
- `query_errors_total`：查询错误数

#### 连接指标

- `connection_total`：连接总数
- `connection_errors_total`：连接错误数

#### 缓存指标

- `cache_hits_total`：缓存命中数
- `cache_misses_total`：缓存未命中数

### 7.2 监控告警

建议设置以下告警：

1. **查询错误率过高**
   - 阈值：错误率 > 5%
   - 检查：存储后端、网络、配置

2. **查询耗时过长**
   - 阈值：P99 耗时 > 5s
   - 检查：查询优化、存储性能

3. **连接失败**
   - 阈值：连接失败率 > 1%
   - 检查：存储后端状态、网络

---

## 8. 常见错误码

### 8.1 HTTP 错误码

- `400 Bad Request`：请求参数错误
- `404 Not Found`：资源不存在
- `500 Internal Server Error`：服务器内部错误
- `504 Gateway Timeout`：查询超时

### 8.2 业务错误码

参考 `metadata/const.go` 中的错误码定义。

---

## 9. 获取帮助

### 9.1 查看文档

- 架构文档：`docs/architecture.md`
- 模块文档：`docs/modules.md`
- 开发指南：`docs/development-guide.md`

### 9.2 查看日志

```bash
# 查看实时日志
tail -f unify-query.log

# 查看错误日志
grep ERROR unify-query.log | tail -100
```

### 9.3 联系支持

- 提交 Issue
- 联系开发团队

---

## 附录

### A. 常用命令

```bash
# 检查服务状态
ps aux | grep unify-query

# 查看端口占用
netstat -tlnp | grep 10205

# 查看进程资源使用
top -p $(pgrep unify-query)

# 查看日志
tail -f unify-query.log
```

### B. 配置文件示例

参考 `unify-query.yaml` 和 `dist/` 目录下的配置文件示例。

