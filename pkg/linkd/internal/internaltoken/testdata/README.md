# JWT 互操作样本

`interop.json` 使用公开测试密钥，生成器为 Kingeye 当前锁定的 PyJWT 2.13.0。

- `python_legacy`：Kingeye 当前仅包含 `username` 的签发形式。
- `python_timed`：`username`、`iat`、`exp`，同时是 Node 签发器的预期输出。
- `go_timed`：使用 Go JSON 字典排序顺序 `exp`、`iat`、`username` 由 PyJWT 签发，Go 签发结果应逐字节相等。

所有测试固定时间，不需要安装 Python 或访问网络。样本到期不影响固定时钟测试。更新库或协议时用 PyJWT 重新生成并交叉验证，不能只把 Go/Node 自己生成的结果复制为期望值。
