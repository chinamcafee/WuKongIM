# 全流频道迁移后的空历史 API 验收

2026-09-08。继续验证流消息全部排除后、首次正常发送前的公开 API 行为。本轮完成本机 HTTP 验收，没有导入现场服务器或更改停机源。

## 发现与处理

此前直接集群读取返回 `channel: channel not found`，因为全流频道没有保留消息，也没有消息运行元数据。旧迁移工作分支将该错误作为 HTTP 400 返回，消息业务层只识别存储层的 NotFound。

最新 main `ded2c0bd4` 已包含通用修复 `065317e0bcbe8c6bcc0457410f974d0020718db1`（`fix(message): handle empty conversations without errors`）。该修复在成员校验之后，将存储层与路由后的频道不存在错误都映射为空页；同时处理新单聊、批量对齐及空会话未读操作，保留非成员、已移除成员、路由及存储失败的限制。

本轮在独立工作副本上用纯 v3 新建空群验证了 main 已有行为，又在旧迁移分支上复现了相同请求返回 400 的问题。随后将原提交的相关用例代码、单元/集成测试和 FLOW 变更同步到迁移候选。没有新增错误绕过、伪造消息、预置空频道日志或改变 v3 存储/复制逻辑。

## 纯 v3 对照

`TestEmptyGroupHistoryHTTPBeforeFirstSend` 不使用任何 v2 数据，通过 `/channel` 创建普通群和成员，在单节点集群与三节点集群中验证：

- `/channel/messagesync` 返回 HTTP 200、`messages: []`、`more: 0`。
- `/channel/messagesyncbatch` 保持项目对齐、空数组和无错误项。
- `/conversation/sync` 对未激活空群返回空列表。
- 非成员同步仍返回 HTTP 400。
- 所有空读前后均确认消息运行元数据仍不存在，完整重启后重复上述行为。
- 第一次 `/message/send` 分配序号 1，各节点入口都能读到相同消息。

旧迁移分支的相同单节点测试失败记录保留为 `migration-baseline.log`：`/channel/messagesync` 返回 HTTP 400，原因是路由后的频道不存在错误。main 对照通过后才同步现有修复。

## 实际导入后的 HTTP 验收

`TestStreamOnlyMigrationHTTP` 使用原版格式的私有合成副本，将所有普通消息标记为待排除流消息，CMD 同时排除。执行完整 prepare、原生安装和独立 verify，确认目标消息数为 0，然后启动真实产品 HTTP 服务。

单节点来源分别迁入单节点集群、三节点集群，验证：

1. 每个 HTTP 入口的单条历史、批量历史及旧会话同步返回空结果。
2. `/conversations/clearUnread` 与 `setUnread(unread=0)` 成功；非成员历史查询仍被拒绝。
3. 通过正常 `/message/send` 发送两条消息，ACK 序号依次为 1、2，ID 非零且不同。
4. 所有入口逐条核对历史中的 ID、序号、ClientMsgNo、正文及 `more`；旧会话中的最近消息按原 API 倒序排列。
5. 两次完整重启后重复校验，消息无遗漏、重复或序号回退。

这是真实安装产物的 HTTP 验证，不把直接调用集群读接口的 ChannelNotFound 当作 API 成功。

## 证据与边界

私有证据目录：`tmp/server-rehearsal-20260908/empty-history-38/`。包含源/工作副本文档摘要、原提交补丁、迁移分支失败基线、main 对照、单元测试、HTTP 集成测试、FLOW 校验及 SHA-256 清单。最终状态以 `validation.json` 为准。

- 消息、会话、集群适配器和 HTTP 适配器相关完整单元测试通过。
- main 已有新单聊集成测试与新增空群对照在迁移候选通过。
- 实际全流导入的 HTTP 集成测试通过，耗时 39.189 秒。
- 上游补丁反向校验和 `flow-doc-contracts` 通过。

实际客户端 SDK、本地缓存和旧游标切换仍未验收；真实源权威、完整 prepare、插件、现场目标运行和 100 GiB 性能验收仍各有独立门槛。插件按用户要求暂缓。本轮不改变这些门槛，也不授权切流。
