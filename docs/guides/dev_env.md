## 监控链路开发环境准备说明


### pre-commit
- [installation](https://pre-commit.com/#installation)
- config hook
    ```bash
    # install addlicense
    go install github.com/google/addlicense@latest
    pre-commit install -t pre-commit -t commit-msg
    ```
- enjoy it.