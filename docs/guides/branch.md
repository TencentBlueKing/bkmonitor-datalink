# 服务分支管理

## 分支定义

1. master: 主干分支，该分支用于发布 bkop 环境，稳定分支，所有 fork 的来源
2. dev: 开发分支，该分支用于发布 dev 环境，不稳定分支，定期删除
3. master + tag: 根据版本号发布对应 tag，用于发布上云环境，tag 格式为：pkg/${模块名}/v${VERSION}

## 开发流程

1. fork 主仓库 master 分支，到个人空间，完成该功能的本地开发与调试
```
几乎一个功能都是一个人开发，如果实在需要同时开发某个功能可以新建分支的方式进行合作开发
```
2. 提 PR 合并到 dev 分支，自动流水线执行打包发布 dev 环境；
3. 开发环境验证通过之后，提 PR 合并到 master 分支，完成 CR，自动流水线执行打包发布 bkop 环境；
4. 打 tag 交付发布包；

## ci（蓝盾）

1. 打包流水线，github.com 仓库监听 Commit Push Hook (master/dev) 以及 Create Branch Or Tag；
2. 所有 pr 操作 (pull request hook) 会触发蓝盾各个模块的单元测试和 CodeCC 检查，包括 (dev 和 master)