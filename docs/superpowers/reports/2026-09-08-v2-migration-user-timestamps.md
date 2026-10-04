# 原用户管理时间差异与完整归档

日期：2026-09-08；工作树：codex/v2-v3-migration。

full-source-39 已结束，退出码 1，错误为用户副本冲突及活动插件缺少正式兼容映射。
源文件前后摘要一致，未导入目标。不能将这次预检算作全量迁移通过。

## 实际差异

对该工作空间已收集的全部元数据候选重新逐组核对：24786 个用户物理记录中，
仅 3 个逻辑用户存在 5 对副本差异，差异只涉及 CreatedAt / UpdatedAt；
其他已收集元数据没有发现冲突或正式副本缺失。完整消息副本比较尚未执行完毕。

读取原停止镜像保留的 Slot 命令，为三个用户找到每节点 7 条、合计 21 条
AddUser 命令。三个节点的相关命令一致，表中各时间均能对应保留命令的原值；
历史应用为何产生分歧仍未确定，不能声称时间由各副本独立生成。

固定 v2 源码的 store/model.go 将这两个时间编码进用户命令；wkdb/user.go
保留已有创建时间，并写入更新时间。旧 cluster 管理接口使用时间进行显示／排序。
v3 原生 meta.User 只有 UID、Token、DeviceFlag、DeviceLevel，没有对应时间字段。

## 处理与验证边界

按已批准的旧管理数据完整归档范围，新增显式
`metadata.archive_user_timestamps=true`。只排除这两个字段的业务副本比较，
保留所有节点原行及原索引；报告对节点身份和完整原行生成 SHA256，统计用户物理
行数及实际时间字段数，并将报告纳入选择摘要。源数据和 v3 存储逻辑均不改动。

默认仍严格拒绝时间分歧。测试先复现时间冲突，再验证启用选项后完成 prepare、
export、移除源目录访问、全新工作空间从归档重建、原生导入和独立 verify。
负向用例确认 User.PluginNo 或 Device.Token 冲突仍拒绝且不发布 PREPARED；
各用例检查源文件未改变。定向测试通过，耗时 4.034 秒。

迁移 usecase、v2/v3 适配器、app、CLI 与命令入口六个包的相关回归全部通过；
Linux amd64 静态构建和 `git diff --check` 通过。validation.json 与 SHA256SUMS
记录代码、二进制及证据摘要，regression.log 保留实际测试输出。

真实源尚需新计划、新工作空间重新预检；活动插件正式迁移仍是独立阻断。
这里的测试不代表现场业务切换或 100 GiB 性能验收通过。

私有证据目录：`tmp/server-rehearsal-20260908/user-conflicts-42/`。
summary.json 与 conflicts.jsonl 只使用身份摘要；original-user-pairs.jsonl
含原始身份，不能公开。command-comparison.json、command-snapshots.json 记录
原命令一致性及前后源摘要；red-test.log / green-test.log 保留回归前后结果。

后续 full-source-43 已用新计划完成用户时间归档与元数据比较，完整绑定 24786
个原用户物理行及 49572 个时间字段；在后续消息副本检查处停止，未发布 PREPARED。
实际结果见[消息副本定位](2026-09-08-v2-migration-message-replicas.md)，原测试证据不变。
