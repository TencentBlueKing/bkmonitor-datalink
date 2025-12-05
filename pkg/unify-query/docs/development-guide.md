# 开发指南

本文档提供 Unify-Query 项目的开发环境搭建、代码规范、测试指南等信息。

## 目录

1. [开发环境搭建](#1-开发环境搭建)
2. [项目结构](#2-项目结构)
3. [代码规范](#3-代码规范)
4. [开发流程](#4-开发流程)
5. [测试指南](#5-测试指南)
6. [调试技巧](#6-调试技巧)
7. [常见问题](#7-常见问题)

---

## 1. 开发环境搭建

### 1.1 系统要求

- **Go 版本**：1.24+
- **操作系统**：Linux / macOS / Windows
- **内存**：建议 8GB+
- **磁盘**：建议 10GB+ 可用空间

### 1.2 依赖服务

开发环境需要以下服务：

- **Consul**：配置中心（可选，可使用本地配置）
- **Redis**：元数据存储（可选，可使用 Mock）
- **InfluxDB**：时序数据库（可选，用于测试）

### 1.3 环境搭建步骤

#### 1.3.1 安装 Go

```bash
# 下载并安装 Go 1.24+
# macOS
brew install go@1.24

# Linux
wget https://go.dev/dl/go1.24.4.linux-amd64.tar.gz
tar -C /usr/local -xzf go1.24.4.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

#### 1.3.2 克隆项目

```bash
git clone https://github.com/TencentBlueKing/bkmonitor-datalink.git
cd bkmonitor-datalink/pkg/unify-query
```

#### 1.3.3 安装依赖

```bash
# 安装 Go 依赖
go mod download

# 安装开发工具
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/swaggo/swag/cmd/swag@latest
go install mvdan.cc/gofumpt@latest
go install github.com/incu6us/goimports-reviser/v3@latest
```

#### 1.3.4 配置本地环境

创建配置文件 `unify-query.yaml`：

```yaml
consul:
  consul_address: http://127.0.0.1:8500
  kv_base_path: bkmonitorv3/unify-query

redis:
  mode: standalone
  host: 127.0.0.1
  port: 6379
  database: 0

http:
  address: 127.0.0.1
  port: 10205
```

#### 1.3.5 启动依赖服务（Docker）

```bash
# 启动 Consul
docker run -d --name consul -p 8500:8500 consul:latest

# 启动 Redis
docker run -d --name redis -p 6379:6379 redis:latest

# 启动 InfluxDB
docker run -d --name influxdb -p 8086:8086 influxdb:1.8
```

### 1.4 IDE 配置

#### VS Code

推荐插件：
- Go
- Go Test
- Go Doc

配置 `.vscode/settings.json`：

```json
{
  "go.testFlags": ["-v", "-race"],
  "go.lintTool": "golangci-lint",
  "go.lintFlags": ["--fast"]
}
```

#### GoLand

推荐配置：
- 启用 Go Modules
- 配置 Go SDK 路径
- 启用代码格式化（gofumpt）

---

## 2. 项目结构

### 2.1 目录结构

```
unify-query/
├── cmd/              # 命令行入口
├── config/            # 配置管理
├── consul/            # Consul 客户端
├── influxdb/          # InfluxDB 客户端
├── metadata/          # 元数据管理
├── query/             # 查询处理
├── service/           # 服务层
│   ├── http/          # HTTP 服务
│   ├── consul/        # Consul 服务
│   └── ...
├── tsdb/              # 存储抽象层
├── docs/              # 文档
├── main.go            # 程序入口
└── unify-query.yaml   # 配置文件
```

### 2.2 关键文件

- `main.go`：程序入口
- `cmd/root.go`：命令行根命令
- `service/http/handler.go`：HTTP 处理器
- `query/structured/query.go`：结构化查询
- `tsdb/interfaces.go`：存储接口定义

---

## 3. 代码规范

### 3.1 Go 代码规范

遵循 [Go 官方代码规范](https://golang.org/doc/effective_go) 和项目规范。

#### 3.1.1 命名规范

- **包名**：小写，简短，有意义
- **函数名**：驼峰命名，公开函数首字母大写
- **变量名**：驼峰命名，公开变量首字母大写
- **常量**：全大写，单词间用下划线分隔

```go
// 好的示例
package metadata

const DefaultExpiration = time.Minute

type User struct {
    SpaceUID string
}

func GetUser(ctx context.Context) *User {
    // ...
}
```

#### 3.1.2 注释规范

- 所有公开的函数、类型、变量都应该有注释
- 注释应该以被注释的内容开头
- 使用完整的句子

```go
// GetUser 从上下文中获取用户信息
// 如果上下文中不存在用户信息，返回 nil
func GetUser(ctx context.Context) *User {
    // ...
}
```

#### 3.1.3 错误处理

- 错误应该作为最后一个返回值
- 错误信息应该清晰明确
- 使用 `fmt.Errorf` 或 `errors.Wrap` 包装错误

```go
func Query(ctx context.Context, query string) (*Result, error) {
    if query == "" {
        return nil, fmt.Errorf("query cannot be empty")
    }
    
    result, err := executeQuery(ctx, query)
    if err != nil {
        return nil, fmt.Errorf("execute query failed: %w", err)
    }
    
    return result, nil
}
```

### 3.2 项目特定规范

#### 3.2.1 Context 使用

- 所有函数都应该接收 `context.Context` 作为第一个参数
- 使用 context 传递追踪信息、超时控制等

```go
func Query(ctx context.Context, query string) (*Result, error) {
    ctx, span := trace.NewSpan(ctx, "query")
    defer span.End(&err)
    // ...
}
```

#### 3.2.2 日志使用

- 使用 `log` 包进行日志记录
- 日志应该包含足够的上下文信息
- 使用适当的日志级别

```go
log.Infof(ctx, "query executed: %s, duration: %v", query, duration)
log.Warnf(ctx, "query timeout: %s", query)
log.Errorf(ctx, "query failed: %v", err)
```

#### 3.2.3 追踪使用

- 使用 `trace` 包进行分布式追踪
- 为关键操作创建 Span
- 记录关键信息到 Span

```go
ctx, span := trace.NewSpan(ctx, "operation-name")
defer span.End(&err)

span.Set("key", "value")
span.AddEvent("event-name")
```

### 3.3 代码格式化

使用以下工具进行代码格式化：

```bash
# 格式化代码
make fmt

# 检查代码
make lint
```

格式化工具：
- `gofumpt`：代码格式化
- `goimports-reviser`：导入排序
- `golangci-lint`：代码检查

---

## 4. 开发流程

### 4.1 创建新功能

#### 4.1.1 创建分支

```bash
git checkout -b feature/your-feature-name
```

#### 4.1.2 开发代码

1. 编写代码
2. 编写测试
3. 运行测试
4. 格式化代码

#### 4.1.3 提交代码

```bash
# 添加文件
git add .

# 提交
git commit -m "feat: add your feature"

# 推送
git push origin feature/your-feature-name
```

### 4.2 代码审查

- 创建 Pull Request
- 等待代码审查
- 根据反馈修改代码
- 合并到主分支

### 4.3 构建和测试

```bash
# 运行测试
make test

# 构建二进制文件
make build

# 运行调试版本
make debug
```

---

## 5. 测试指南

### 5.1 单元测试

#### 5.1.1 测试文件命名

测试文件以 `_test.go` 结尾，例如 `query_test.go`。

#### 5.1.2 测试函数命名

测试函数以 `Test` 开头，例如 `TestQuery`。

```go
func TestQuery(t *testing.T) {
    ctx := context.Background()
    result, err := Query(ctx, "test query")
    if err != nil {
        t.Fatalf("query failed: %v", err)
    }
    if result == nil {
        t.Fatal("result is nil")
    }
}
```

#### 5.1.3 测试表

使用测试表进行多场景测试：

```go
func TestQuery(t *testing.T) {
    tests := []struct {
        name    string
        query   string
        wantErr bool
    }{
        {
            name:    "valid query",
            query:   "cpu_usage",
            wantErr: false,
        },
        {
            name:    "empty query",
            query:   "",
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := Query(context.Background(), tt.query)
            if (err != nil) != tt.wantErr {
                t.Errorf("Query() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### 5.2 集成测试

集成测试需要真实的依赖服务，可以使用 Docker Compose 启动测试环境。

```go
func TestIntegration(t *testing.T) {
    // 启动测试服务
    // ...
    
    // 执行测试
    // ...
    
    // 清理
    // ...
}
```

### 5.3 Mock 测试

使用 `github.com/golang/mock` 生成 Mock 对象：

```bash
# 生成 Mock
go generate ./...
```

使用 Mock：

```go
func TestWithMock(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()
    
    mockInstance := NewMockInstance(ctrl)
    mockInstance.EXPECT().QueryRange(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
        Return(&tsdb.PromData{}, nil)
    
    // 使用 Mock 进行测试
    // ...
}
```

### 5.4 运行测试

```bash
# 运行所有测试
make test

# 运行特定包的测试
go test ./query/...

# 运行带覆盖率的测试
go test -cover ./...

# 运行带竞态检测的测试
make race
```

---

## 6. 调试技巧

### 6.1 使用日志

在代码中添加详细的日志：

```go
log.Debugf(ctx, "query params: %+v", params)
log.Infof(ctx, "query executed: %s", query)
log.Warnf(ctx, "query slow: %v", duration)
```

### 6.2 使用追踪

使用 OpenTelemetry 追踪查询链路：

```go
ctx, span := trace.NewSpan(ctx, "operation")
defer span.End(&err)

span.Set("query", query)
span.Set("duration", duration)
```

### 6.3 使用调试器

#### Delve

```bash
# 安装 Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# 启动调试
dlv debug ./main.go

# 设置断点
(dlv) break handler.go:100

# 运行
(dlv) continue

# 查看变量
(dlv) print variable
```

#### VS Code

1. 创建 `.vscode/launch.json`
2. 设置断点
3. 按 F5 启动调试

### 6.4 性能分析

使用 `pprof` 进行性能分析：

```go
import _ "net/http/pprof"

// 在代码中启动 pprof
go func() {
    log.Println(http.ListenAndServe("localhost:6060", nil))
}()
```

使用 `go tool pprof` 分析：

```bash
go tool pprof http://localhost:6060/debug/pprof/profile
```

---

## 7. 常见问题

### 7.1 编译问题

**问题**：`go: cannot find module providing package`

**解决**：
```bash
go mod download
go mod tidy
```

### 7.2 测试问题

**问题**：测试超时

**解决**：增加测试超时时间
```bash
go test -timeout 5m ./...
```

### 7.3 依赖问题

**问题**：依赖版本冲突

**解决**：
```bash
go mod tidy
go mod vendor  # 如果需要 vendor
```

### 7.4 配置问题

**问题**：配置不生效

**解决**：
1. 检查配置文件路径
2. 检查配置文件格式
3. 检查配置项名称
4. 重启服务

### 7.5 连接问题

**问题**：无法连接 Consul/Redis

**解决**：
1. 检查服务是否启动
2. 检查网络连接
3. 检查配置地址和端口
4. 检查防火墙设置

---

## 8. 开发工具

### 8.1 Makefile 命令

```bash
make build      # 构建二进制文件
make debug      # 构建调试版本
make test       # 运行测试
make fmt        # 格式化代码
make lint       # 代码检查
make swag       # 生成 Swagger 文档
```

### 8.2 代码生成

```bash
# 生成 Mock
go generate ./...

# 生成 Swagger 文档
make swag
```

### 8.3 代码检查

```bash
# 运行所有检查
make lint

# 只运行格式化
make fmt
```

---

## 9. 贡献指南

### 9.1 提交规范

使用 [Conventional Commits](https://www.conventionalcommits.org/) 规范：

- `feat:` 新功能
- `fix:` 修复 bug
- `docs:` 文档更新
- `style:` 代码格式
- `refactor:` 重构
- `test:` 测试
- `chore:` 构建/工具

### 9.2 Pull Request

1. 创建清晰的问题描述
2. 提供测试用例
3. 更新相关文档
4. 确保所有测试通过
5. 等待代码审查

---

## 附录

### A. 参考资源

- [Go 官方文档](https://golang.org/doc/)
- [Go 代码规范](https://golang.org/doc/effective_go)
- [Go 测试文档](https://golang.org/pkg/testing/)

### B. 相关文档

- [架构设计文档](./architecture.md)
- [核心模块文档](./modules.md)
- [存储引擎集成文档](./storage-integration.md)

