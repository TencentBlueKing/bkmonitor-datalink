### 功能
influxdb实例管理模块，通过manager统一管理backend(influxdb实例)，提供给其他模块(cluster)使用

### 目录
influxdb 文件夹
存放backend接口的具体实现(也是目前的唯一实现),influxdb_backend
authorazation.go influxdb实例认证接口
define.go 存放接口定义
errors.go 存放错误定义
factory.go 存放backend工厂方法，所有backend实例通过该工厂方法取出
hook.go 存放一些触发逻辑
manager.go 存放manager的具体实现，manager负责管理全局backend实例列表
pointsreader.go  存放CopyReader的具体实现--PointsReader，该实现是针对inflxudb-proxy数据读写的性能优化方案
struct.go 存放一些实际类型，通常为具体参数类型
utils.go 存放一些工具方法