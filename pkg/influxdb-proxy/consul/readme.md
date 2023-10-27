### 功能
consul操作模块，高度封装针对consul的所有操作

### 概念
consul 动态配置组件，具备健康检查，kv存取等能力，详情请查看：https://www.consul.io/docs

### 目录
base 文件夹,存放更底层的针对consul的操作
backend.go 封装提供给backend模块的consul操作
cluster.go 封装提供给cluster模块的consul操作
route.go 封装提供给route模块的consul操作
tag.go 封装提供给cluster/routecluster模块的，与tag相关的consul操作
consul.go 封装全局使用的consul操作
errors.go 错误信息
struct.go 存放参数结构体
utils.go 存放一些工具方法

