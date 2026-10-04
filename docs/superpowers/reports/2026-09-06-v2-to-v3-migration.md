# v2 → v3 迁移本机验收记录

日期：2026-09-06。环境：macOS / arm64，Go 1.25.0。
工作分支：`codex/v2-v3-migration`，实现基于 v3 `ab1ced72450658ba85399b03293ac37c20278721`。
本记录随候选源码提交固化；不是已发布版本或生产数据验收证明。
[独立审查与修复记录](2026-09-06-v2-to-v3-migration-review.md)保留两个审查轴及回归证据。

原版 v2 为未经修改的 `v2.2.5-20260422`，提交
`a888f89533d0e7d1b2030e06504ca97f1ad891d4`。fixture 来自其公共 wkdb 接口或
真实服务进程，包含独立保存的 API 观察、源配置和 SHA-256；
[来源记录](../../../internal/infra/migrationv2/testdata/README.md)包含生成方式与限制。

## 结果

| 验证层 | 结果与覆盖 |
| --- | --- |
| 原版源读取 | 通过；格式、业务索引/原分片、身份映射、文件清单、只读锁、停止节点、会话尾部恢复、权威选择与副本比较 |
| 业务转换与归档 | 通过；完整协议、用户/设备、频道/成员/权限、普通/CMD 会话、事件状态/游标、归档往返 |
| 恢复与负面测试 | 通过；部分元数据/消息导入重试，源/计划变化拒绝，非本次目标拒绝，目标改动保护，未应用尾部拒绝 |
| 独立全量校验 | 通过；逐字段核对、全部副本及额外记录、ID/幂等索引、LEO/HW、完整 proposal 摘要链、Controller/Slot 启动快照 |
| 校验器破坏性用例 | 通过；篡改凭据、中间日志身份及启动快照均失败；错误不回显 token |
| 原生复制集成 | 通过；记录缺失前缀边界、完整消息字段、双预算边界、大消息空副本修复、目标重启与续写 |
| 产品进程黑盒 | 通过；1→1、1→3、3→1、3→3、3→5，原 token/错误 token、历史、CMD、会话/事件同步、新 ID/seq、幂等、重启、多节点目标 Leader 故障后的读写 |
| 空群边界 | 通过；原版已有会话更新时间但无消息的群，迁后保持不可见；原成员发送首条消息 seq=1 后可见 |
| 权限拒绝 | 通过；原黑名单用户返回 InBlacklist，非成员返回 SubscriberNotExist，均不生成 ID/seq |
| 文档规则与构建 | 通过；FLOW 79 个合规、0 无效、0 警告；差异空白检查、两个二进制构建、CLI help |

黑盒测试使用 256 hash slots、4 个目标物理 Slot，源单节点主样本有 64 个 Slot，
健康三节点来源有 4 个 Slot。覆盖节点数量变化不等于每一种物理 Slot 数量与数据
分布都做过性能验收。SDK 路径使用仓库 Go WKProto 客户端；不是所有第三方 SDK
版本的验收矩阵。

## 可复现检查

以下命令在本工作副本执行通过；依赖不包含生产服务。

```sh
GOWORK=off go test ./pkg/db/message ./internal/infra/migrationv2 ./internal/infra/migrationv3 ./internal/usecase/migration ./internal/access/migratecli ./internal/app/migration ./cmd/wkmigrate -count=1
GOWORK=off go test ./pkg/channel/... ./pkg/cluster/... ./pkg/quorumlog/... ./internal/usecase/message ./internal/usecase/conversation ./internal/infra/cluster ./internal/access/api ./internal/app ./internal/access/migratecli ./internal/app/migration ./cmd/wkmigrate -count=1
GOWORK=off go test ./pkg/raftlog ./pkg/controller/raft/raftstore -count=1
GOWORK=off go test ./internal/usecase/message -run TestEventSync -count=1
GOWORK=off go test ./pkg/cluster/channels -run TestRepairScanner -count=1
GOWORK=off go test -tags=integration ./internal/infra/migrationv2 -run TestOriginalMigration -count=1 -timeout=3m
GOWORK=off go test -tags=integration ./internal/infra/migrationv2 -run TestOriginalV2Writer -count=1 -timeout=1m
GOWORK=off go test -tags=integration ./pkg/channel/replication -run TestImportedHistoryRecoversThroughPublicQuorumRuntime -count=1 -timeout=2m
GOWORK=off go test -tags=e2e ./test/e2e/migration/original_v2 -count=1 -timeout=5m
GOWORK=off go test -tags=e2e ./test/e2e/migration/original_v2 -run TestOriginalV2Archive -count=1 -timeout=3m
GOWORK=off go run ./scripts/flowcheck --mode check
git diff --check
GOWORK=off go build -o ./bin/wkmigrate ./cmd/wkmigrate
GOWORK=off go build -o ./bin/wukongim-v3 ./cmd/wukongim
./bin/wkmigrate --help
```

审查修复后的完整黑盒矩阵及空群用例耗时 132.341 秒，包含新增 3→5 拓扑。
原生目标启动/消息副本恢复与源写锁集成 20.134 秒，前缀 quorum 恢复集成
1.643 秒。这些是小样本测试时长，不是迁移吞吐或停机窗口估算。

## 实际 CLI 报告

额外用构建好的二进制，在隔离目录对 `original-v2-server.tar.gz` 执行
`prepare → export → import → verify`，生成
[原始 verify JSON](v2-migration-example-verify.json)。全部四个命令退出码为 0。

该单节点集群样本核对 4 条消息（3 条普通消息和 1 条 CMD）、2 个用户、2 个设备、
4 个普通/权限/系统频道、6 条订阅者、2 条普通 membership、2 条 CMD membership、
2 条频道运行元数据及各 1 条事件状态、消息游标和事件幂等记录。
`status` 为 `offline_verified`，`cutover_ready` 为 `false`。

固定提交后的迁移工具与 v3 服务成对构建，产物另附记录源码提交、平台、Go 版本
和各文件 SHA-256 的 manifest。不要混用审查前构建的工具或迁入产物；先前不符合
原生恢复预算的已完成输出现在会被校验拒绝，应使用新工作空间和新目标重做。
已部署 v2 保持原版；Linux 运行验收仍需目标环境。

## 使用边界与待验收项

- 100 GiB 源业务数据、停写至验证完成且可切换 ≤4 小时：**未执行**，按用户约定
  等待测试环境。本机测试不作为这一性能承诺的依据。
- 每个物理 Slot 的原生元数据快照桥接目前上限 256 MiB；事件投影页上限 8 MiB。
  消息 proposal 的原生编码及传输计费均须在 1 MiB 恢复预算内，单条超限会拒绝。
  超限会拒绝；需在实际规模验收时评估内存、磁盘、放大和总停机时间。
- 业务绑定、全局方法或有效配置的插件缺少映射、关键业务索引损坏、独立非零未读计数、CMD 删除边界、无状态事件游标、
  无法确定成员历史可见性、副本冲突或未排空通知等情况，预检明确拒绝。
- 当前验证过消息目录丢失后的副本恢复；整节点 Raft 数据清空后复用旧 node ID
  属于另一种集群重入场景，未验证，不能照此操作。
- TLS、网关鉴权开关、有效配置、插件程序、Webhook 与业务方 SDK 需要真实部署
  验收。通知队列为空不证明历史外部通知已全部交付，工具不会重放旧通知。
- 导入产物只能用于全新目标集群；首次启动前完成离线校验，隔离启动通过业务验收
  后再切流。v3 接收新业务写入后不能直接切回旧 v2，工具不提供反向增量迁移。

具体流程、计划格式和恢复方法见[操作手册](../runbooks/v2-to-v3-migration.md)。
