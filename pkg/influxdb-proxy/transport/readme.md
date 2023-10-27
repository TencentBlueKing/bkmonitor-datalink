### 功能
数据迁移模块，负责在tag发生rebalance后，重新迁移数据

### 目录
const.go 存放一些常量
error.go 存放错误信息
influxdb.go 存放有关influxdb的操作封装
transport.go 提供检查consul，并根据实际情况触发迁移任务的功能

