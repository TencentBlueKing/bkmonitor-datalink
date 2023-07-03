### 功能
cluster实例管理模块，通过manager统一管理

### 概念
cluster(集群): 对influxdb-proxy的使用者隐藏的概念，主要功能为多写和负载均衡，使用者的每一次读写请求都会被influxdb-proxy分析成为针对cluster的读写,而使用者自身体感为向单个influxdb实例进行读写

### 目录
routecluster 文件夹  存放cluster目前唯一的实现方案
define.go 存放接口定义
errors.go 存放错误定义
factory.go 存放cluster工厂方法，所有cluster实例通过该工厂方法取出
hook.go 存放一些触发逻辑
manager.go 存放manager的具体实现，manager负责管理全局cluster实例列表
struct.go 存放一些实际类型，通常为具体参数类型
utils.go 存放一些工具方法

