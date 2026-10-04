# WuKongEasySDK validation history

Captured from WuKongIM `96dc9758f421db39ae5f0d0153c3911be7c10c4c` on 2026-09-08 before the reader-guide rewrite.

This is engineering reference, not the current installation guide. The original package, source, harness, server, environment, failed observations and bounded claims are retained together. Historical npm 2.0.4 runs are not 2.0.5 runs. Commands below are historical reproduction instructions, not authorization to trigger workflows.

Current Web tutorials pin npm 2.0.5. Its independent released-package C#/JS WS, native Node and Chromium WSS record is preserved under CSharp below; it does not update the earlier four-platform matrix.


## index


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/index.mdx)

WuKongEasySDK 通过 WebSocket JSON-RPC CONNECT 提供轻量连接与在线消息 API。它适合已经拥有业务后端、希望自己维护界面和产品状态的应用。

#### 选择平台，发送第一条消息

<Cards>
  <Card title="运行官方示例" description="按已验证 revision 启动四端 example，复现构建、双向消息与清理。" href="/zh/sdk/easy/examples" />
  <Card title="iOS 快速接入" description="精确安装 1.1.1，用一个应用级客户端完成连接、收发与清理。" href="/zh/sdk/easy/ios/getting-started" />
  <Card title="Android 快速接入" description="精确安装 1.0.5，按进程单例与 Activity 生命周期完成闭环。" href="/zh/sdk/easy/android/getting-started" />
  <Card title="Flutter 快速接入" description="精确安装 1.1.0，保存监听器引用并在 dispose 中释放。" href="/zh/sdk/easy/flutter/getting-started" />
  <Card title="Web 快速接入" description="精确安装 easyjssdk 2.0.4，通过业务 BFF 获取连接材料。" href="/zh/sdk/easy/javascript/getting-started" />
  <Card title="Rust 快速接入" description="安装 crates.io 0.1.0，使用 Tokio 管理收发、有限重连与清理。" href="/zh/sdk/easy/rust/getting-started" />
  <Card title="C# 快速接入" description="从固定源码接入 .NET 8，使用异步 API、独立实例和 await using 完成收发。" href="/zh/sdk/easy/csharp/getting-started" />
  <Card title="C++ 快速接入" description="预编译包或 vcpkg 接入，以 C++17 和 CMake 完成在线消息和线程清理。" href="/zh/sdk/easy/cpp/getting-started" />
  <Card title="Python 快速接入" description="固定 PyPI 版本 + asyncio，完成在线收发、心跳、重连和资源清理。" href="/zh/sdk/easy/python/getting-started" />
</Cards>

完成对应教程后，你会让 Alice 与 Bob 分别连接，在个人 Channel 中双向发送一个文本 JSON Payload，并在退出时正确移除监听器和连接。

#### 先判断是否适合

| 选择 EasySDK，如果你只需要 | 选择完整版 WuKongIMSDK，如果你还需要 |
| --- | --- |
| WebSocket 连接与自动重连 | 本地消息数据库与离线恢复 |
| 单聊或群聊 Channel 的在线消息 | 会话列表、未读数与消息同步 |
| 发送结果和实时消息事件 | 推送、多设备与完整平台能力 |
| 自己维护 UI、持久化和业务回执 | SDK 承担更多客户端消息状态 |

EasySDK 不是聊天 UI，也不是完整产品后端。`send` 成功表示服务端返回了发送结果，不表示对方已经收到、展示或处理消息。需要完整能力时，回到 [SDK 选择](https://docs.githubim.com/zh/sdk)。

#### 开始前：由业务后端提供连接材料

客户端不应该创建自己的身份或调用 Product HTTP 管理接口。用户登录产品后，受信业务后端通过 HTTPS 返回最小连接材料：

```json
{
  "uid": "alice",
  "token": "short-lived-token",
  "websocketUrl": "wss://im.example.com/ws"
}
```

| 字段 | 所有者与约束 |
| --- | --- |
| `uid` | 业务后端确认的稳定用户标识；Alice 与 Bob 必须不同 |
| `token` | 短期、可撤销，只用于当前身份连接；不能授予 Product HTTP 管理权限 |
| `websocketUrl` | 后端从部署配置或安全路由结果中选择；生产环境使用 `wss://` |

先完成[认证与 Token](https://docs.githubim.com/zh/guide/integration/authentication)。当前默认组合会把 CONNECT Token 与 `/user/token` 保存的相同 UID、设备类别记录精确匹配；部署仍必须保护该接口、实现过期与轮换策略，并证明无效、撤销和按业务规则过期的 Token 会被拒绝。

#### Alice 与 Bob 验收闭环

各平台遵循同一条最小路径：

1. 为 Alice 和 Bob 分别取得自己的 `uid`、短期 `token` 与 `websocketUrl`；
2. 在两个独立设备、进程或浏览器上下文中按平台教程创建客户端；
3. 两端都观察到连接成功后，Alice 向个人 Channel `bob` 发送文本 JSON Payload；
4. Alice 保存发送结果，Bob 在实时消息事件中核对 `fromUid`、Channel 与 Payload；
5. Bob 向 Alice 回发，验证反向链路；
6. 退出页面或账号，移除监听器并断开连接，确认没有重复事件或后台连接。

这个闭环只验证在线消息。发送确认、实时接收与业务完成是三个不同状态，具体建模见[消息收发](https://docs.githubim.com/zh/guide/integration/messaging)。

希望先验证环境时，直接按[运行官方示例](https://docs.githubim.com/zh/sdk/easy/examples)使用已通过的服务端和客户端 revision，不必先把教学代码移入自己的应用。

#### 各平台生命周期差异

| 任务 | iOS | Android | Flutter | Web | C# |
| --- | --- | --- | --- | --- | --- |
| 创建与初始化 | 应用级 `WuKongEasySDK` 实例 | 进程单例 `getInstance()` + `init` | 应用级单例 + `init` | 每个身份或浏览器上下文一个 `WKIM` 实例 | 独立 `WKIM` 实例 |
| 监听器归属 | 保存 `EventListener` token | 保存同一个 listener 对象 | 保存同一个回调引用 | `on` / `off` 使用同一个函数引用 | 使用相同委托 `+=` / `-=` |
| 允许发送 | `onConnect` 后 | `CONNECT` 后 | `connect` 事件后 | `Connect` 事件后 | `ConnectAsync` 成功后 |
| 退出账号 | 移除 token 并 `disconnect` | 移除监听并断开；账号切换需处理单例限制 | 移除监听、`disconnect`、`dispose` | `off` 后 `destroy` | 等待 `DisposeAsync` / `await using` |

页面可以订阅连接状态，但不应因为页面重建而创建第二条连接。平台教程中的完整最小代码分别展示了正确的 owner 和清理位置。

C++ 使用独立 `WKIM` 实例和 I/O 线程，保存 `ListenerId`，退出时移除监听并等待 `disconnect()` / `destroy()` 完成。回调中不能等待 SDK future，UI 和耗时工作应交给应用执行器。详见 [C++ 快速接入](https://docs.githubim.com/zh/sdk/easy/cpp/getting-started)。

#### 上线前还要完成

- 使用 WSS，并验证证书、反向代理 WebSocket Upgrade 与设备侧可达性；
- 验证 Token 过期、撤销、错误身份和重放都会被拒绝；
- 在 Release 构建中保持 SDK 诊断关闭，并用非生产 canary 检查设备日志、Console、崩溃报告和采集器；
- 分别验收断网重连、离线恢复、去重、推送、多设备、容量、监控、升级与回滚；
- 记录服务端 revision、SDK 版本、平台、设备和网络环境，不把一次开发闭环当作生产 receipt。

继续使用[上线检查](https://docs.githubim.com/zh/guide/integration/acceptance)关闭这些门禁。

#### 版本与证据

  2026 年 8 月 31 日，四个官方 example 连接 WuKongIM `5676700d2dc966fa6fc9b2f0620a6ae429adad5a` 完成源码运行；9 月 1 日，npm `2.0.4`、Maven Central `1.0.5`、CocoaPods `1.1.1` 与 pub.dev `1.1.0` 又连接 PR 最终 HEAD `1c9430f15fc8844e7025df07d54ab6e80e026414` 的测试合并服务端 `35f314cc2512f3f0f5d55d9677e817cb64129985`，完成 Alice/Bob 在线双向消息和断开清理。托管任务见[正式包验收运行](https://github.com/WuKongIM/WuKongIM/actions/runs/33484491015)，浏览器包另在 Chrome 151 通过。这仍不是物理真机、WSS、离线能力或生产 Token 校验凭据。


| 平台 | 已验证 example revision | 与正式版本的关系 |
| --- | --- | --- |
| Web | [`a055b3667247333b6b3183249f5d5929673dfd53`](https://github.com/WuKongIM/WuKongEasySDK-JS/commit/a055b3667247333b6b3183249f5d5929673dfd53) | 已包含在正式 `v2.0.4` |
| Android | [`7134bbd0263fd01d9e7f71b7bd05b226f75b2292`](https://github.com/WuKongIM/WuKongEasySDK-Android/commit/7134bbd0263fd01d9e7f71b7bd05b226f75b2292) | 已包含在正式 `v1.0.5` |
| iOS | [`40014c16c0becd390c105098d359048901f4d87c`](https://github.com/WuKongIM/WuKongEasySDK-iOS/commit/40014c16c0becd390c105098d359048901f4d87c) | 已包含在正式 `v1.1.1` |
| Flutter | [`98ab8f3d9a1ad53f40c32caef0979845a37ae9a6`](https://github.com/WuKongIM/WuKongEasySDK-Flutter/commit/98ab8f3d9a1ad53f40c32caef0979845a37ae9a6) | 与正式 `v1.1.0` 相同 |

  上表证明精确源码 example；正式包运行证明 Registry 实际解析到的归档。新补丁版本已经包含这些源码修复并通过正式包验收，但记录结果时仍要同时保留源码 revision、包版本、服务端 revision 与运行环境。


##### 当前正式发布版本

| 平台 | 固定版本与源码 revision | 正式分发 |
| --- | --- | --- |
| [iOS](https://github.com/WuKongIM/WuKongEasySDK-iOS) | `v1.1.1` · `ca688fcac2c4cd8d6f8e8163faf165376b520ba9` | [Release](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/tag/v1.1.1) · [CocoaPods](https://cocoapods.org/pods/WuKongEasySDK) |
| [Android](https://github.com/WuKongIM/WuKongEasySDK-Android) | `v1.0.5` · `61ae6dc6d0077b15e47cda1fd530296b97a06a7a` | [Release](https://github.com/WuKongIM/WuKongEasySDK-Android/releases/tag/v1.0.5) · [Maven Central](https://central.sonatype.com/artifact/com.githubim/easysdk-android/1.0.5) |
| [Flutter](https://github.com/WuKongIM/WuKongEasySDK-Flutter) | `v1.1.0` · `98ab8f3d9a1ad53f40c32caef0979845a37ae9a6` | [Release](https://github.com/WuKongIM/WuKongEasySDK-Flutter/releases/tag/v1.1.0) · [pub.dev](https://pub.dev/packages/wukong_easy_sdk/versions/1.1.0) |
| [Web](https://github.com/WuKongIM/WuKongEasySDK-JS) | `v2.0.4` · `9c03c98c725982fac224cd1d3b52456eae983975` | [Release](https://github.com/WuKongIM/WuKongEasySDK-JS/releases/tag/v2.0.4) · [npm](https://www.npmjs.com/package/easyjssdk/v/2.0.4) |

##### 正式包运行凭据

| 平台 | 精确 Registry 产物 | 运行环境 | 结果 |
| --- | --- | --- | --- |
| Web | `easyjssdk@2.0.4` | Chrome 151；托管 Node.js 对端 | 双向消息、SENDACK 与断开通过 |
| Android | `com.githubim:easysdk-android:1.0.5` | Android 14 / API 34 Emulator | Maven 解析、instrumentation 双向消息与断开通过 |
| iOS | `WuKongEasySDK 1.1.1` | iOS Simulator | CocoaPods 解析、双向消息与断开通过 |
| Flutter | `wukong_easy_sdk 1.1.0` | iOS Simulator | pub.dev hosted 解析、双向消息与断开通过 |

应用依赖只使用这些精确版本，不使用 `latest` 或宽松版本范围。四个正式版本都已包含默认关闭、输出脱敏的日志安全修复：

| 平台 | 修复来源 |
| --- | --- |
| iOS | [PR #3](https://github.com/WuKongIM/WuKongEasySDK-iOS/pull/3) · `b7ec4440b940539bee213f95a3be74948f4b9fb8` |
| Android | [PR #3](https://github.com/WuKongIM/WuKongEasySDK-Android/pull/3) · `e984c7374a0e11f5d109ad3dbfdea599907735ff` |
| Flutter | [PR #3](https://github.com/WuKongIM/WuKongEasySDK-Flutter/pull/3) · `d7758c301e5289ddfa09cd09b6976c2479584b1c` |
| Web | [PR #6](https://github.com/WuKongIM/WuKongEasySDK-JS/pull/6) · `3ebf505734c5b6764b30eac011f0b7a5024c89e8` |

当前教程的任务顺序校准自旧版 [EasySDK 概览](https://wukong.mintlify.app/zh/sdk/easy/overview)、[iOS](https://wukong.mintlify.app/zh/sdk/easy/ios/getting-started)、[Android](https://wukong.mintlify.app/zh/sdk/easy/android/getting-started)、[Flutter](https://wukong.mintlify.app/zh/sdk/easy/flutter/getting-started)和 [Web](https://wukong.mintlify.app/zh/sdk/easy/javascript/getting-started) 页面；API、版本与安全边界以上表的正式源码和分发产物为准。

#### C# / NuGet 接入

[WuKongEasySDK-CSharp](https://github.com/WuKongIM/WuKongEasySDK-CSharp) 提供 .NET 8+ 的独立客户端，NuGet 包为 `WuKongEasySDK` `1.0.0`。使用 `new WKIM` / `WKIM.Init` 创建实例，`ConnectAsync` 认证后调用 `SendAsync`，通过 `+=` / `-=` 管理事件，退出时等待 `DisposeAsync` 或使用 `await using`。默认设备类别为 PC `2`。

C# 已发布到 [nuget.org](https://www.nuget.org/packages/WuKongEasySDK/1.0.0)，请按 [C# 快速接入](https://docs.githubim.com/zh/sdk/easy/csharp/getting-started) 安装精确版本 `1.0.0`；也可使用固定 commit 的项目引用或本地 NuGet 包。它的真实进程验证使用 WuKongIM `132e46209`，与上面的四端历史正式包记录分别保留。

应用侧地址切换、三节点手动复现与服务端阻塞状态见 [C# 集群恢复说明](https://docs.githubim.com/zh/sdk/easy/csharp/getting-started#三节点集群与应用切换地址)。

#### C++ 预编译包、vcpkg 与固定源码

[C++ 仓库](https://github.com/WuKongIM/WuKongEasySDK-CPP) 工程版本 `0.1.0`，本文固定源码 `3e367a908f42385ab9306f9708b7456399cace7d`，可通过本仓库维护的 vcpkg Git registry 自动安装 SDK 及依赖，或使用 CMake 源码安装；也可下载 [v0.1.0 预编译包](https://github.com/WuKongIM/WuKongEasySDK-CPP/releases/tag/v0.1.0)，解压接入 Windows x64、macOS arm64 和 Linux x64；该 registry 不属于微软默认目录。它参考 JS `v2.0.4`，已在 WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d` 上完成 C++/C++ 和 C++/JS 在线双向消息、重连、错误 Token 拒绝和在线清理，并通过独立的 WS/WSS 协议测试。环境与完整验证范围见 [C++ 验证记录](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/3e367a908f42385ab9306f9708b7456399cace7d/docs/VALIDATION.md)。该源码证据独立于上面四端的历史正式包验收。

#### 下一步

先[运行官方示例](https://docs.githubim.com/zh/sdk/easy/examples)，再选择目标平台完成应用接入。需要离线消息、会话、未读和更完整的平台 API 时，改用[完整版 SDK](https://docs.githubim.com/zh/sdk/wukongim)。

#### Python PyPI 接入

[WuKongEasySDK-Python](https://github.com/WuKongIM/WuKongEasySDK-Python) 使用 Python 3.11+ 与 asyncio，通过 [PyPI `wukong-easy-sdk==0.1.0`](https://pypi.org/project/wukong-easy-sdk/0.1.0/) 安装。已在 WuKongIM `0348c0539bbee420a859439695acdac911afa854`、开启 Token 鉴权的 256 Hash Slot 单节点集群上完成 Python/Python 与真实 JS `2.0.4` 双向消息、心跳、重新连接、错误 Token 拒绝和在线清理。详细步骤与验证范围见 [Python 快速接入](https://docs.githubim.com/zh/sdk/easy/python/getting-started)。本次使用从 PyPI 新安装的正式包，文件哈希、测试环境和互通结果见 [Python 正式包验证记录](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/90018a9ac53bdef4cb15dadb169ff835cbfcb2a6/docs/PYPI_VALIDATION.md)；该验收独立于四端历史正式包记录。

后续同一 PyPI 包的三节点 WSS 30 分钟运行与故障恢复见[独立验收记录](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/a9be49e69f4b7155a9a6244ef63f9cb6baf00cb9/docs/CLUSTER_VALIDATION.md)；其中明确区分长时间运行、短 CI 和各自的固定版本。

Python 还提供[群聊示例与验证记录](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/4d39f9e43265f88415f2fe01fc5418182e405c52/docs/GROUP_VALIDATION.md)：四个 Python/JS 客户端在初始 Leader 已收敛的三节点 WSS 上验证成员增删、群隔离、权限与重连。该场景要求记录中的服务端成员缓存修复源码，安装包仍为 PyPI `0.1.0`；具体后端准备与 CLI 命令见 [Python 群聊接入](https://docs.githubim.com/zh/sdk/easy/python/getting-started#7-在线群聊与成员权限)。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/index.en.mdx)

WuKongEasySDK uses WebSocket JSON-RPC CONNECT for a lightweight connection and online-messaging API. It fits applications that already have a product backend and own their UI and product state.

#### Choose a platform and send the first message

<Cards>
  <Card title="Run the official examples" description="Reproduce builds, bidirectional messaging, and cleanup at verified revisions." href="/en/sdk/easy/examples" />
  <Card title="iOS quickstart" description="Install exactly 1.1.1 and use one application-owned client for connect, messaging, and cleanup." href="/en/sdk/easy/ios/getting-started" />
  <Card title="Android quickstart" description="Install exactly 1.0.5 and complete the loop around the process singleton and Activity lifecycle." href="/en/sdk/easy/android/getting-started" />
  <Card title="Flutter quickstart" description="Install exactly 1.1.0, retain listener references, and release them from dispose." href="/en/sdk/easy/flutter/getting-started" />
  <Card title="Web quickstart" description="Install exactly easyjssdk 2.0.4 and obtain connection material through a product BFF." href="/en/sdk/easy/javascript/getting-started" />
  <Card title="Rust quickstart" description="Install crates.io 0.1.0 and use Tokio for messaging, bounded reconnect and cleanup." href="/en/sdk/easy/rust/getting-started" />
  <Card title="C# quickstart" description="Use pinned .NET 8 source, async APIs, independent clients, and await using for messaging." href="/en/sdk/easy/csharp/getting-started" />
  <Card title="C++ quickstart" description="Prebuilt packages or vcpkg with CMake for C++17 messaging and cleanup." href="/en/sdk/easy/cpp/getting-started" />
  <Card title="Python quickstart" description="Pinned PyPI version and asyncio for online messaging, heartbeat, reconnect, and cleanup." href="/en/sdk/easy/python/getting-started" />
</Cards>

After the platform tutorial, Alice and Bob will connect separately, exchange a text JSON payload in a person Channel, then remove listeners and connections on exit.

#### Check whether it fits

| Choose EasySDK when you only need | Choose the full WuKongIMSDK when you also need |
| --- | --- |
| WebSocket connection and automatic reconnect | A local message database and offline recovery |
| Online messages in person or group Channels | Conversations, unread counts, and message synchronization |
| Send results and realtime message events | Push, multi-device behavior, and the broader platform API |
| Product-owned UI, persistence, and receipts | More client message state owned by the SDK |

EasySDK is neither a chat UI nor a complete product backend. A successful `send` means the server returned a send result; it does not mean the peer received, displayed, or processed the message. If you need the broader feature set, return to [SDK selection](https://docs.githubim.com/en/sdk).

Rust uses one `Client` per identity; clones share the socket. `subscribe()` returns a bounded receiver, `disconnect().await` permits later connection, and `destroy().await` closes permanently. The default device category is PC `2`. See the [Rust quickstart](https://docs.githubim.com/en/sdk/easy/rust/getting-started) for installation and lifecycle.

#### Before you begin: get connection material from your backend

The client must not create its own identity or call Product HTTP management routes. After product login, the trusted backend returns the minimum connection material over HTTPS:

```json
{
  "uid": "alice",
  "token": "short-lived-token",
  "websocketUrl": "wss://im.example.com/ws"
}
```

| Field | Owner and constraint |
| --- | --- |
| `uid` | Stable user identity confirmed by the product backend; Alice and Bob use different values |
| `token` | Short-lived and revocable, scoped to this identity's connection, with no Product HTTP management authority |
| `websocketUrl` | Selected by the backend from deployment configuration or a trusted routing result; production uses `wss://` |

Complete [Authentication & Tokens](https://docs.githubim.com/en/guide/integration/authentication) first. The default composition exactly matches each CONNECT token against the `/user/token` record for the same UID and device category. Your deployment must still protect that route, implement expiry and rotation policy, and prove that invalid, revoked, and product-expired tokens are rejected.

#### Alice/Bob acceptance loop

All platforms follow the same minimal path:

1. Obtain separate `uid`, short-lived `token`, and `websocketUrl` values for Alice and Bob.
2. Create the clients in two independent devices, processes, or browser contexts by following the platform quickstart.
3. After both sides report a successful connection, have Alice send a text JSON payload to the person Channel `bob`.
4. Retain Alice's send result; on Bob, verify `fromUid`, the Channel, and the payload in the realtime message event.
5. Send from Bob to Alice and prove the reverse direction.
6. Leave the page or sign out, remove listeners, and disconnect; confirm there is no duplicate event or background connection.

This loop verifies online messaging only. Send acknowledgement, realtime receipt, and product completion are three different states; model them with [Messaging](https://docs.githubim.com/en/guide/integration/messaging).

To validate the environment first, follow [Run the Official Examples](https://docs.githubim.com/en/sdk/easy/examples) with the verified server and client revisions before moving tutorial code into your application.

#### Lifecycle differences across platforms

| Task | iOS | Android | Flutter | Web | C# |
| --- | --- | --- | --- | --- | --- |
| Create and initialize | Application-owned `WuKongEasySDK` instance | Process singleton via `getInstance()` + `init` | Application-owned singleton + `init` | One `WKIM` instance per identity or browser context | Independent `WKIM` instance |
| Listener ownership | Retain each `EventListener` token | Retain the same listener object | Retain the same callback reference | Pass the same function reference to `on` and `off` | Use the same delegate with `+=` / `-=` |
| Allow send | After `onConnect` | After `CONNECT` | After the `connect` event | After the `Connect` event | After `ConnectAsync` succeeds |
| Account exit | Remove tokens and `disconnect` | Remove listeners and disconnect; account switching must handle the singleton limit | Remove listeners, `disconnect`, then `dispose` | `off`, then `destroy` | Await `DisposeAsync` / `await using` |

A view may subscribe to connection state, but rebuilding the view must not create a second connection. Each platform's complete minimal example shows the correct owner and cleanup point.

C++ uses an independent `WKIM` instance and I/O thread. Retain `ListenerId` values, remove listeners at exit, and wait for `disconnect()` / `destroy()` to finish. Callbacks must never wait on SDK futures; dispatch UI and slow work to the application executor. See the [C++ quickstart](https://docs.githubim.com/en/sdk/easy/cpp/getting-started).

#### Complete before production

- Use WSS and verify certificates, reverse-proxy WebSocket Upgrade, and device-side reachability.
- Prove that expired, revoked, wrong-identity, and replayed tokens are rejected.
- Keep SDK diagnostics disabled in Release builds, then use non-production canaries to inspect device logs, the browser Console, crash reports, and collectors.
- Accept reconnect, offline recovery, deduplication, push, multi-device behavior, capacity, observability, upgrades, and rollback separately.
- Record the server revision, SDK version, platform, device, and network instead of treating one development loop as a production receipt.

Use [Release Checks](https://docs.githubim.com/en/guide/integration/acceptance) to close these gates.

#### Versions and evidence

  On August 31, 2026, all four official examples completed source runs against WuKongIM `5676700d2dc966fa6fc9b2f0620a6ae429adad5a`. On September 1, npm `2.0.4`, Maven Central `1.0.5`, CocoaPods `1.1.1`, and pub.dev `1.1.0` then connected to test-merge server `35f314cc2512f3f0f5d55d9677e817cb64129985` for final PR head `1c9430f15fc8844e7025df07d54ab6e80e026414` and completed Alice/Bob online bidirectional messaging plus disconnect cleanup. See the [hosted released-package run](https://github.com/WuKongIM/WuKongIM/actions/runs/33484491015); the browser package also passed separately in Chrome 151. This is still not physical-device, WSS, offline, or production-token evidence.


| Platform | Verified example revision | Relationship to the release |
| --- | --- | --- |
| Web | [`a055b3667247333b6b3183249f5d5929673dfd53`](https://github.com/WuKongIM/WuKongEasySDK-JS/commit/a055b3667247333b6b3183249f5d5929673dfd53) | Included in released `v2.0.4` |
| Android | [`7134bbd0263fd01d9e7f71b7bd05b226f75b2292`](https://github.com/WuKongIM/WuKongEasySDK-Android/commit/7134bbd0263fd01d9e7f71b7bd05b226f75b2292) | Included in released `v1.0.5` |
| iOS | [`40014c16c0becd390c105098d359048901f4d87c`](https://github.com/WuKongIM/WuKongEasySDK-iOS/commit/40014c16c0becd390c105098d359048901f4d87c) | Included in released `v1.1.1` |
| Flutter | [`98ab8f3d9a1ad53f40c32caef0979845a37ae9a6`](https://github.com/WuKongIM/WuKongEasySDK-Flutter/commit/98ab8f3d9a1ad53f40c32caef0979845a37ae9a6) | The same source as released `v1.1.0` |

  The table above proves exact source examples; the released-package run proves the archives actually resolved from each registry. The patch releases now include those source fixes and pass package acceptance, but retain the source revision, package version, server revision, and runtime whenever recording a result.


##### Current released versions

| Platform | Pinned release and source revision | Official distribution |
| --- | --- | --- |
| [iOS](https://github.com/WuKongIM/WuKongEasySDK-iOS) | `v1.1.1` · `ca688fcac2c4cd8d6f8e8163faf165376b520ba9` | [Release](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/tag/v1.1.1) · [CocoaPods](https://cocoapods.org/pods/WuKongEasySDK) |
| [Android](https://github.com/WuKongIM/WuKongEasySDK-Android) | `v1.0.5` · `61ae6dc6d0077b15e47cda1fd530296b97a06a7a` | [Release](https://github.com/WuKongIM/WuKongEasySDK-Android/releases/tag/v1.0.5) · [Maven Central](https://central.sonatype.com/artifact/com.githubim/easysdk-android/1.0.5) |
| [Flutter](https://github.com/WuKongIM/WuKongEasySDK-Flutter) | `v1.1.0` · `98ab8f3d9a1ad53f40c32caef0979845a37ae9a6` | [Release](https://github.com/WuKongIM/WuKongEasySDK-Flutter/releases/tag/v1.1.0) · [pub.dev](https://pub.dev/packages/wukong_easy_sdk/versions/1.1.0) |
| [Web](https://github.com/WuKongIM/WuKongEasySDK-JS) | `v2.0.4` · `9c03c98c725982fac224cd1d3b52456eae983975` | [Release](https://github.com/WuKongIM/WuKongEasySDK-JS/releases/tag/v2.0.4) · [npm](https://www.npmjs.com/package/easyjssdk/v/2.0.4) |

##### Released-package runtime evidence

| Platform | Exact registry artifact | Runtime | Result |
| --- | --- | --- | --- |
| Web | `easyjssdk@2.0.4` | Chrome 151; hosted Node.js peer | Bidirectional messaging, SENDACK, and disconnect passed |
| Android | `com.githubim:easysdk-android:1.0.5` | Android 14 / API 34 Emulator | Maven resolution, instrumentation bidirectional messaging, and disconnect passed |
| iOS | `WuKongEasySDK 1.1.1` | iOS Simulator | CocoaPods resolution, bidirectional messaging, and disconnect passed |
| Flutter | `wukong_easy_sdk 1.1.0` | iOS Simulator | pub.dev hosted resolution, bidirectional messaging, and disconnect passed |

Application dependencies use only these exact versions—never `latest` or a broad version range. All four official releases include logging-security changes that leave diagnostics off by default and sanitize enabled output:

| Platform | Fix provenance |
| --- | --- |
| iOS | [PR #3](https://github.com/WuKongIM/WuKongEasySDK-iOS/pull/3) · `b7ec4440b940539bee213f95a3be74948f4b9fb8` |
| Android | [PR #3](https://github.com/WuKongIM/WuKongEasySDK-Android/pull/3) · `e984c7374a0e11f5d109ad3dbfdea599907735ff` |
| Flutter | [PR #3](https://github.com/WuKongIM/WuKongEasySDK-Flutter/pull/3) · `d7758c301e5289ddfa09cd09b6976c2479584b1c` |
| Web | [PR #6](https://github.com/WuKongIM/WuKongEasySDK-JS/pull/6) · `3ebf505734c5b6764b30eac011f0b7a5024c89e8` |

The task sequence was calibrated from the legacy [EasySDK overview](https://wukong.mintlify.app/en/sdk/easy/overview), [iOS](https://wukong.mintlify.app/en/sdk/easy/ios/getting-started), [Android](https://wukong.mintlify.app/en/sdk/easy/android/getting-started), [Flutter](https://wukong.mintlify.app/en/sdk/easy/flutter/getting-started), and [Web](https://wukong.mintlify.app/en/sdk/easy/javascript/getting-started) pages. The released source and distributions above remain authoritative for APIs, versions, and security boundaries.

#### C# / NuGet integration

[WuKongEasySDK-CSharp](https://github.com/WuKongIM/WuKongEasySDK-CSharp) provides an independent .NET 8+ client through the `WuKongEasySDK` `1.0.0` NuGet package. Create it with `new WKIM` / `WKIM.Init`, authenticate with `ConnectAsync`, send with `SendAsync`, manage events with `+=` / `-=`, and await `DisposeAsync` or use `await using` on exit. Its default device category is PC `2`.

C# is available on [nuget.org](https://www.nuget.org/packages/WuKongEasySDK/1.0.0). Follow the [C# quickstart](https://docs.githubim.com/en/sdk/easy/csharp/getting-started) to install exact version `1.0.0`, or build a pinned project reference or local NuGet package. Its real-process verification uses WuKongIM `132e46209` and remains separate from the four historical registry-package receipts above.

See [C# cluster recovery](https://docs.githubim.com/en/sdk/easy/csharp/getting-started#three-node-clusters-and-application-address-replacement) for application endpoint replacement, optional three-node reproduction, and tracked server blockers.

#### C++ prebuilt packages, vcpkg and pinned source

The [C++ repository](https://github.com/WuKongIM/WuKongEasySDK-CPP) has project version `0.1.0`. This tutorial pins source `3e367a908f42385ab9306f9708b7456399cace7d`, installed through the WuKongIM-maintained vcpkg Git registry or CMake source integration; [v0.1.0 prebuilt archives](https://github.com/WuKongIM/WuKongEasySDK-CPP/releases/tag/v0.1.0) also support Windows x64, macOS arm64 and Linux x64. The registry is not Microsoft’s curated catalog. It follows JS `v2.0.4` and completed C++/C++ and C++/JS online bidirectional messaging, reconnect, invalid-token rejection, and presence cleanup against WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d`, plus separate WS/WSS protocol tests. See the [C++ validation record](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/3e367a908f42385ab9306f9708b7456399cace7d/docs/VALIDATION.md) for environment and scope. This source evidence is separate from the historical four-platform released-package acceptance above.

#### Next

First [run the official examples](https://docs.githubim.com/en/sdk/easy/examples), then choose a platform for application integration. If you need offline messages, conversations, unread state, or broader platform APIs, switch to the [full SDK](https://docs.githubim.com/en/sdk/wukongim).

#### Python PyPI integration

[WuKongEasySDK-Python](https://github.com/WuKongIM/WuKongEasySDK-Python) uses Python 3.11+ and asyncio and is installed from [PyPI as `wukong-easy-sdk==0.1.0`](https://pypi.org/project/wukong-easy-sdk/0.1.0/). It completed Python/Python and actual JS `2.0.4` bidirectional messaging, heartbeat, reconnect, invalid-Token rejection, and online cleanup against WuKongIM `0348c0539bbee420a859439695acdac911afa854`, a Token-authenticated 256-hash-slot single-node cluster. See the [Python quickstart](https://docs.githubim.com/en/sdk/easy/python/getting-started) for steps and validation scope. This run used a fresh PyPI package installation; see the [Python package validation record](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/90018a9ac53bdef4cb15dadb169ff835cbfcb2a6/docs/PYPI_VALIDATION.md) for artifact hashes, test environments, and interoperability results. This acceptance is independent of the historical four-platform package receipts.

See the [separate acceptance record](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/a9be49e69f4b7155a9a6244ef63f9cb6baf00cb9/docs/CLUSTER_VALIDATION.md) for the subsequent three-node WSS 30-minute run and fault recovery of the same PyPI package; it distinguishes the long run, short CI and their exact versions.

Python also provides a [group example and validation record](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/4d39f9e43265f88415f2fe01fc5418182e405c52/docs/GROUP_VALIDATION.md): four Python/JS clients verify membership changes, isolation, permissions and reconnect over three-node WSS after initial leader convergence. This scenario requires the recorded server recipient-cache fix; the installed PyPI package remains `0.1.0`. See the [Python group quickstart](https://docs.githubim.com/en/sdk/easy/python/getting-started#7-online-group-messaging-and-permissions) for backend setup and CLI commands.


## examples


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/examples.mdx)

本页记录两类可复现凭据。2026 年 8 月 31 日，四个官方仓库的源码 example 都已连接 WuKongIM `5676700d2dc966fa6fc9b2f0620a6ae429adad5a`，服务端进程级 JSON-RPC E2E 也在该 revision 通过。9 月 1 日，从 npm、Maven Central、CocoaPods 与 pub.dev 解析的四个正式包连接 PR 最终 HEAD `1c9430f15fc8844e7025df07d54ab6e80e026414` 的测试合并服务端 `35f314cc2512f3f0f5d55d9677e817cb64129985`，完成 Alice → Bob、Bob → Alice 与断开清理。

  源码运行证明精确仓库 revision，正式包运行证明 Registry 实际提供的归档。Web `v2.0.4`、Android `v1.0.5` 与 iOS `v1.1.1` 已包含先前验证的 example 和连接生命周期修复，Flutter 继续使用 `v1.1.0`；记录结果时仍要同时保留两类凭据。


#### 已验证结果

##### 正式发布包（2026-09-01）

| 平台 | 正式包与发布源码 | 构建与运行环境 | 实际结果 |
| --- | --- | --- | --- |
| Web | [`easyjssdk@2.0.4`](https://www.npmjs.com/package/easyjssdk/v/2.0.4) · [`9c03c98c725982fac224cd1d3b52456eae983975`](https://github.com/WuKongIM/WuKongEasySDK-JS/commit/9c03c98c725982fac224cd1d3b52456eae983975) | Chrome 151；托管 Node.js 对端 | 浏览器与正式包对端的双向消息、SENDACK 和断开通过 |
| Android | [`com.githubim:easysdk-android:1.0.5`](https://central.sonatype.com/artifact/com.githubim/easysdk-android/1.0.5) · [`61ae6dc6d0077b15e47cda1fd530296b97a06a7a`](https://github.com/WuKongIM/WuKongEasySDK-Android/commit/61ae6dc6d0077b15e47cda1fd530296b97a06a7a) | JDK 17、Android 14 / API 34 托管模拟器 | Maven 解析、instrumentation 双向消息和断开通过 |
| iOS | [`WuKongEasySDK 1.1.1`](https://cocoapods.org/pods/WuKongEasySDK) · [`ca688fcac2c4cd8d6f8e8163faf165376b520ba9`](https://github.com/WuKongIM/WuKongEasySDK-iOS/commit/ca688fcac2c4cd8d6f8e8163faf165376b520ba9) | CocoaPods 1.16.2、iOS Simulator | Pod 解析、双向消息和断开通过 |
| Flutter | [`wukong_easy_sdk 1.1.0`](https://pub.dev/packages/wukong_easy_sdk/versions/1.1.0) · [`98ab8f3d9a1ad53f40c32caef0979845a37ae9a6`](https://github.com/WuKongIM/WuKongEasySDK-Flutter/commit/98ab8f3d9a1ad53f40c32caef0979845a37ae9a6) | Flutter 3.41.4、iOS Simulator | hosted 依赖解析、双向消息和断开通过 |

Android、iOS 与 Flutter 的托管任务以及每项共用的 npm `2.0.4` 对端见[最终 HEAD 正式包验收运行](https://github.com/WuKongIM/WuKongIM/actions/runs/33484491015)。

##### 官方源码 example（2026-08-31）

| 平台 | 官方 example 源码 | 构建与运行环境 | 实际结果 |
| --- | --- | --- | --- |
| Web | [`a055b3667247333b6b3183249f5d5929673dfd53`](https://github.com/WuKongIM/WuKongEasySDK-JS/tree/a055b3667247333b6b3183249f5d5929673dfd53/example) | Node.js 22.12、Chrome 151、macOS | 61 个测试通过，ESM/CJS 构建通过，浏览器双向消息、断开与重连通过 |
| Android | [`7134bbd0263fd01d9e7f71b7bd05b226f75b2292`](https://github.com/WuKongIM/WuKongEasySDK-Android/tree/7134bbd0263fd01d9e7f71b7bd05b226f75b2292/example) | JDK 17、Gradle 8.4、Android 14 / API 34 模拟器 | Gradle 测试与 Debug 构建通过，双向消息、手动断开和心跳超时语义通过 |
| iOS | [`40014c16c0becd390c105098d359048901f4d87c`](https://github.com/WuKongIM/WuKongEasySDK-iOS/tree/40014c16c0becd390c105098d359048901f4d87c/Examples/WuKongIMExample-Unified) | Xcode 16.2、iPhone 16 / iOS 18.3 Simulator | 30 个测试与 Release 构建通过，macOS/iOS 脚本通过，双向消息正文与时间显示正确 |
| Flutter | [`98ab8f3d9a1ad53f40c32caef0979845a37ae9a6`](https://github.com/WuKongIM/WuKongEasySDK-Flutter/tree/98ab8f3d9a1ad53f40c32caef0979845a37ae9a6/example) (`v1.1.0`) | Flutter 3.41.4、Dart 3.11.1、iOS Simulator | 25 个测试与静态分析通过，官方 example 双向消息通过 |

服务端还通过了固定的进程级回归测试：

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/easy_sdk_jsonrpc \
  -count=1 -timeout 2m -p=1 -v
```

这份结果覆盖单节点集群、默认 256 个 Hash Slot、JSON-RPC CONNECT、Ping、SEND/SENDACK、RECV/RECVACK、在线双向消息和客户端清理。它不覆盖物理真机、WSS 代理、生产 Token 拒绝、离线同步、推送、多设备、容量或长期稳定性。

#### 1. 启动同一版 WuKongIM

在一个终端中启动开发用单节点集群：

```bash
git clone https://github.com/WuKongIM/WuKongIM.git
cd WuKongIM
git checkout 5676700d2dc966fa6fc9b2f0620a6ae429adad5a
cp wukongim.toml.example wukongim.toml
go run ./cmd/wukongim -config ./wukongim.toml
```

另开终端检查就绪状态：

```bash
curl -fsS http://127.0.0.1:5001/readyz
```

默认开发入口是 Product HTTP `http://127.0.0.1:5001` 与 EasySDK WebSocket `ws://127.0.0.1:5200`。生产环境必须改用受保护的 HTTPS/WSS 入口。

#### 2. 准备 Alice 与 Bob

由受信业务后端为两个用户准备各自的 `uid`、`token` 和 WebSocket 地址。只在本机回环开发环境中，可以由受信终端调用 [`POST /user/token`](https://docs.githubim.com/zh/api/product-http/users/setQuickstartUserToken) 建立测试身份。以下以两个原生客户端为例；如果某个用户由 Web example 登录，把该用户的 `device_flag` 改为 `1`：

```bash
curl -fsS -X POST http://127.0.0.1:5001/user/token \
  -H 'Content-Type: application/json' \
  -d '{"uid":"alice","token":"alice-token","device_flag":0,"device_level":1}'

curl -fsS -X POST http://127.0.0.1:5001/user/token \
  -H 'Content-Type: application/json' \
  -d '{"uid":"bob","token":"bob-token","device_flag":0,"device_level":1}'
```

原生移动端使用 APP `0`，Web 使用 WEB `1`，桌面端使用 PC `2`。默认产品装配会精确校验相同 UID 与设备类别的已存 Token；生产部署仍需保护该接口、实现过期与轮换策略，并单独证明无效、撤销和按业务规则过期的 Token 会被拒绝。

根据客户端运行位置填写 WebSocket 地址：

| 客户端 | 本机开发地址 |
| --- | --- |
| 浏览器、Node.js、macOS App、iOS Simulator | `ws://127.0.0.1:5200` |
| Android Emulator | `ws://10.0.2.2:5200` |
| 物理手机 | `ws://<开发机局域网 IP>:5200`，并确认防火墙与路由可达 |

#### 3. 运行平台 example

##### Web

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-JS.git
cd WuKongEasySDK-JS
git checkout 9c03c98c725982fac224cd1d3b52456eae983975
npm ci
npm test
npm run build
python3 -m http.server 8080
```

打开 `http://127.0.0.1:8080/example/`。用两个隔离的浏览器上下文分别填写 Alice 与 Bob；不要在 Console 中打印 Token 或完整 Payload。

##### Android

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-Android.git
cd WuKongEasySDK-Android
git checkout 61ae6dc6d0077b15e47cda1fd530296b97a06a7a
./gradlew test :example:assembleDebug
./gradlew :example:installDebug
```

在 API 21+ 设备或模拟器中启动 `example`。Android Emulator 连接宿主机时使用 `ws://10.0.2.2:5200`，不是 `localhost`。需要两个原生身份时使用两台设备或两个独立模拟器；也可以让浏览器 example 作为另一端。

##### iOS 与 macOS

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-iOS.git
cd WuKongEasySDK-iOS
git checkout ca688fcac2c4cd8d6f8e8163faf165376b520ba9
swift test
swift build -c release
cd Examples/WuKongIMExample-Unified
./build.sh macos
./build.sh ios
```

先启动一个 iOS Simulator，再运行：

```bash
./build.sh ios --run
```

也可以用 `./build.sh macos --run` 启动 macOS 版本。示例中的 iOS ATS 明文例外只用于本地 `ws://` 开发，不能复制到生产 App。

##### Flutter

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-Flutter.git
cd WuKongEasySDK-Flutter
git checkout 98ab8f3d9a1ad53f40c32caef0979845a37ae9a6
flutter pub get
flutter analyze
flutter test
cd example
flutter pub get
flutter devices
flutter run -d <device-id>
```

为每个实际发布目标分别运行，不要用 iOS Simulator 结果替代 Android、Web 或桌面端验收。

当前 Web、Android 与 iOS 发布提交只在已验证源码之上修改版本、变更日志或发布说明，example 行为源码保持不变；Flutter 发布源码与先前验证 revision 相同。若要核对 8 月 31 日的精确源码凭据，使用上方“官方源码 example”表中的 revision。

##### C# / .NET 源码示例

新增的 [WuKongEasySDK-CSharp](https://github.com/WuKongIM/WuKongEasySDK-CSharp) 使用 .NET 8，NuGet 正式包为 [WuKongEasySDK 1.0.0](https://www.nuget.org/packages/WuKongEasySDK/1.0.0)。它不属于上面四端的历史正式包记录。

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-CSharp.git
cd WuKongEasySDK-CSharp
git checkout d365a354f5e0f25fbd7f83bb59aa365ba43e899f
dotnet build -c Release
dotnet test -c Release
dotnet run --project examples/ConsoleChat -- bob
```

运行前设置 `WUKONGIM_WS_URL`、`WUKONGIM_UID`、`WUKONGIM_TOKEN`；两个终端分别使用 Alice/Bob 的凭据。后端准备 Token 时将 `device_flag` 设为 `2`，与 C# 默认 `DeviceFlag.Desktop` 一致。完整示例见 [C# 快速接入](https://docs.githubim.com/zh/sdk/easy/csharp/getting-started)。

C# 源码在 macOS arm64 / .NET `8.0.424` 通过 35 项测试和本地 NuGet 打包，并对 WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d` 完成启用 Token 认证的单节点集群双向消息、重连、心跳、错误 Token 拒绝与清理。可使用 `WUKONGIM_BINARY=/absolute/path/to/wukongim python3 scripts/smoke.py` 复现；脚本仅操作自己启动的回环地址测试进程。该记录不代表公共 NuGet 下载或生产 WSS 验证。

应用侧地址切换、三节点手动复现与服务端阻塞状态见 [C# 集群恢复说明](https://docs.githubim.com/zh/sdk/easy/csharp/getting-started#三节点集群与应用切换地址)。

#### 4. 重跑正式包自动验收

仓库中的 `Safety Automation - EasySDK Released Package Acceptance` 会从四个 Registry 解析精确版本，构建当前 WuKongIM，再用正式 npm 包作为 Bob，分别在 Android API 34 与两个 iOS Simulator 任务中完成双向消息。需要重跑时可手动触发：

```bash
gh workflow run easysdk-release-acceptance.yml --ref main
```

工作流会检查 Gradle、Podfile.lock 与 Dart package config 的实际来源，避免本地源码依赖冒充正式包。真实浏览器仍应按 Web 教程单独验收。

#### 5. 完成双向验收

1. 两端都观察到连接成功，再启用发送按钮；
2. Alice 向个人 Channel `bob` 发送 `{"type":1,"content":"hello bob"}`；
3. Alice 看到 SEND 完成，Bob 核对 `fromUid`、`channelId`、`messageId`、正文和时间；
4. Bob 向个人 Channel `alice` 回发，完成反向链路；
5. 主动断开再连接，确认每次只发出一个连接/断开事件，消息监听没有重复；
6. 停止服务端或断开网络，确认客户端在有界时间内进入失败或重连状态，而不是永久停在 Connecting；
7. 退出页面或账号，移除监听器并执行平台对应的 `disconnect`、`dispose` 或 `destroy`。

发送成功、对端实时收到、对端展示和业务处理是四个不同状态。消息恢复和去重还要按[消息收发](https://docs.githubim.com/zh/guide/integration/messaging)单独设计。

#### 常见问题

- **Android 模拟器连接失败**：把 `localhost` 改为 `10.0.2.2`；真机改用开发机局域网地址。
- **检出 tag 后现象与上表不同**：先确认 `git rev-parse HEAD` 与包管理器锁文件都对应当前固定版本；不要让本地 `path`、`project(':')` 或缓存依赖替代 Registry 产物。
- **SEND 成功但另一端没有消息**：确认目标 Channel ID 就是对方 UID、两端都在线，且接收端没有因重复 listener 或错误去重丢弃 RECV。
- **iOS 或 Android 构建要求接受 License**：先按本机 Xcode、Android SDK Manager 的提示接受对应 License 并下载目标 runtime，再重跑原命令。
- **本地 Token 能连接**：这只证明内置精确匹配成功。继续验证错误、撤销、按业务规则过期的 Token 会被拒绝，并明确有效 Token 的重放限制。

#### 下一步

根据目标平台继续阅读 [iOS](https://docs.githubim.com/zh/sdk/easy/ios/getting-started)、[Android](https://docs.githubim.com/zh/sdk/easy/android/getting-started)、[Flutter](https://docs.githubim.com/zh/sdk/easy/flutter/getting-started) 或 [Web](https://docs.githubim.com/zh/sdk/easy/javascript/getting-started) 快速接入，再用[上线验收](https://docs.githubim.com/zh/guide/integration/acceptance)补齐 WSS、真机、离线、容量与回滚证据。

#### Rust 源码示例（2026-09-08）

Rust 仓库为 [WuKongIM/WuKongEasySDK-Rust](https://github.com/WuKongIM/WuKongEasySDK-Rust)，已发布 [crates.io 正式包 `0.1.0`](https://crates.io/crates/wukong-easy-sdk/0.1.0)。应用使用 `wukong-easy-sdk = "=0.1.0"` 安装；以下保留仓库源码示例。按 [Rust 快速接入](https://docs.githubim.com/zh/sdk/easy/rust/getting-started) 使用固定 revision `5b4a59cdbb66a9e0c3878e73ba4656f08ee05c6b`，运行 `cargo run --locked --example chat` 或 `cargo run --locked --example roundtrip`；两组 Rust 身份的 `device_flag` 均为 PC `2`。

验证源码 `5b4a59cdbb66a9e0c3878e73ba4656f08ee05c6b` 已在 Linux CI 连接修复 WebSocket 缓冲区问题的 WuKongIM `27a39f15bf163b433f417b78ab6bfc6e589585e5`，完成 Rust/Rust、Rust/`easyjssdk@2.0.4` 120 秒 WSS Unicode 回显、三次断网恢复、错误 Token 拒绝与清理。JS 对端使用 WEB `1`，集群为 256 Hash Slot 单节点集群。正式包已另行通过空缓存下载、校验和比对及文档示例编译；详见 [Rust 验证记录](https://docs.githubim.com/zh/sdk/easy/rust/getting-started#验证记录)。这些源码结果独立于上方四端正式包凭据，不代表容量或多日稳定性。

#### C++ 预编译包、vcpkg 与源码示例

下载 [v0.1.0 预编译包](https://github.com/WuKongIM/WuKongEasySDK-CPP/releases/tag/v0.1.0) 后，可按 [C++ 快速接入](https://docs.githubim.com/zh/sdk/easy/cpp/getting-started) 编译包内 `example`，无需安装 vcpkg。包内 `wukong_chat` 使用相同的 Token 和命令行参数。

C++ 工程版本 `0.1.0` 推荐通过 [vcpkg 接入](https://docs.githubim.com/zh/sdk/easy/cpp/getting-started)，由包管理器自动安装 SDK 及依赖；下面保留源码交互示例，独立于上面的四端历史正式包验收。两个终端分别通过 `WKIM_TOKEN` 提供业务后端签发的本人 Token。

```sh
git clone https://github.com/WuKongIM/WuKongEasySDK-CPP.git
cd WuKongEasySDK-CPP
git checkout 3e367a908f42385ab9306f9708b7456399cace7d
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --config Release --parallel 2
ctest --test-dir build -C Release --output-on-failure
./build/wukong_chat ws://127.0.0.1:5200 alice bob
./build/wukong_chat ws://127.0.0.1:5200 bob alice
```

两端都连接后双向输入文本，分别观察 `SEND completed` 和对端 `Message`，输入 `/quit` 清理。默认设备类别为 Desktop `2`，后端 Token 必须对应相同类别；默认开发配置使用 `/` 路径。依赖、Windows 命令、私有 CA 和线程约束见 [C++ quickstart](https://docs.githubim.com/zh/sdk/easy/cpp/getting-started).

2026-09-08 的源码验收使用 WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d`、Token 鉴权及 256 hash slots，完成 C++/C++、C++/JS 消息、重连、错误 Token 拒绝和在线清理。完整环境与 WS/WSS 证据见 [C++ validation](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/3e367a908f42385ab9306f9708b7456399cace7d/docs/VALIDATION.md).

#### Python：使用固定 PyPI 包运行示例

Python 教程使用 Python 3.11+ 与 asyncio，固定 [PyPI `wukong-easy-sdk==0.1.0`](https://pypi.org/project/wukong-easy-sdk/0.1.0/)。按 [Python 快速接入](https://docs.githubim.com/zh/sdk/easy/python/getting-started) 创建虚拟环境、安装正式包并下载 `v0.1.0` 示例源码。两个终端分别设置 `WKIM_UID`、`WKIM_TOKEN`、`WKIM_PEER`、`WKIM_URL` 后运行：

```sh
python WuKongEasySDK-Python/examples/chat.py
```

Python 默认设备类别为 PC/Desktop `2`。两人均显示 `Connected` 后双向发送，`/quit` 退出。PyPI 安装包验收连接的是 WuKongIM `0348c0539bbee420a859439695acdac911afa854`，不是本页历史四端正式包使用的服务端版本。开启 Token 鉴权的 256 Hash Slot 单节点集群上，Python/Python 和真实 JS `2.0.4` 双向消息、心跳、重新连接、错误 Token 拒绝与在线清理通过。复现独立的进程级验收：

```sh
python WuKongEasySDK-Python/tests/product.py --server /absolute/path/to/wukongim \
  --js-entry /absolute/path/to/WuKongEasySDK-JS/dist/cjs/index.js
```

该脚本仅启动临时本机集群，通过公开接口设置测试身份并检查在线清理，最后停止自己的进程。WSS、自动重连和队列边界使用独立协议测试；完整范围见 [Python 验证记录](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/90018a9ac53bdef4cb15dadb169ff835cbfcb2a6/docs/PYPI_VALIDATION.md)。

后续同一 PyPI 包的三节点 WSS 30 分钟运行与故障恢复见[独立验收记录](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/a9be49e69f4b7155a9a6244ef63f9cb6baf00cb9/docs/CLUSTER_VALIDATION.md)；其中明确区分长时间运行、短 CI 和各自的固定版本。

Python 还提供[群聊示例与验证记录](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/4d39f9e43265f88415f2fe01fc5418182e405c52/docs/GROUP_VALIDATION.md)：四个 Python/JS 客户端在初始 Leader 已收敛的三节点 WSS 上验证成员增删、群隔离、权限与重连。该场景要求记录中的服务端成员缓存修复源码，安装包仍为 PyPI `0.1.0`；具体后端准备与 CLI 命令见 [Python 群聊接入](https://docs.githubim.com/zh/sdk/easy/python/getting-started#7-在线群聊与成员权限)。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/examples.en.mdx)

This page records two reproducible evidence classes. On August 31, 2026, the source examples in all four official repositories connected to WuKongIM `5676700d2dc966fa6fc9b2f0620a6ae429adad5a`; the server process-level JSON-RPC E2E also passed at that revision. On September 1, the packages resolved from npm, Maven Central, CocoaPods, and pub.dev connected to test-merge server `35f314cc2512f3f0f5d55d9677e817cb64129985` for final PR head `1c9430f15fc8844e7025df07d54ab6e80e026414` and completed Alice → Bob, Bob → Alice, and disconnect cleanup.

  A source run proves one repository revision; a released-package run proves the archive actually served by a registry. Web `v2.0.4`, Android `v1.0.5`, and iOS `v1.1.1` now include the previously verified example and connection-lifecycle fixes, while Flutter remains on `v1.1.0`. Keep both evidence classes in every result.


#### Verified results

##### Released packages (September 1, 2026)

| Platform | Released package and release source | Build and runtime | Observed result |
| --- | --- | --- | --- |
| Web | [`easyjssdk@2.0.4`](https://www.npmjs.com/package/easyjssdk/v/2.0.4) · [`9c03c98c725982fac224cd1d3b52456eae983975`](https://github.com/WuKongIM/WuKongEasySDK-JS/commit/9c03c98c725982fac224cd1d3b52456eae983975) | Chrome 151; hosted Node.js peer | Browser and released-package-peer bidirectional messaging, SENDACK, and disconnect passed |
| Android | [`com.githubim:easysdk-android:1.0.5`](https://central.sonatype.com/artifact/com.githubim/easysdk-android/1.0.5) · [`61ae6dc6d0077b15e47cda1fd530296b97a06a7a`](https://github.com/WuKongIM/WuKongEasySDK-Android/commit/61ae6dc6d0077b15e47cda1fd530296b97a06a7a) | JDK 17, Android 14 / API 34 hosted emulator | Maven resolution, instrumentation bidirectional messaging, and disconnect passed |
| iOS | [`WuKongEasySDK 1.1.1`](https://cocoapods.org/pods/WuKongEasySDK) · [`ca688fcac2c4cd8d6f8e8163faf165376b520ba9`](https://github.com/WuKongIM/WuKongEasySDK-iOS/commit/ca688fcac2c4cd8d6f8e8163faf165376b520ba9) | CocoaPods 1.16.2, iOS Simulator | Pod resolution, bidirectional messaging, and disconnect passed |
| Flutter | [`wukong_easy_sdk 1.1.0`](https://pub.dev/packages/wukong_easy_sdk/versions/1.1.0) · [`98ab8f3d9a1ad53f40c32caef0979845a37ae9a6`](https://github.com/WuKongIM/WuKongEasySDK-Flutter/commit/98ab8f3d9a1ad53f40c32caef0979845a37ae9a6) | Flutter 3.41.4, iOS Simulator | Hosted dependency resolution, bidirectional messaging, and disconnect passed |

The Android, iOS, and Flutter hosted jobs—and the released npm `2.0.4` peer shared by each—are recorded in the [final-head released-package acceptance run](https://github.com/WuKongIM/WuKongIM/actions/runs/33484491015).

##### Official source examples (August 31, 2026)

| Platform | Official example source | Build and runtime | Observed result |
| --- | --- | --- | --- |
| Web | [`a055b3667247333b6b3183249f5d5929673dfd53`](https://github.com/WuKongIM/WuKongEasySDK-JS/tree/a055b3667247333b6b3183249f5d5929673dfd53/example) | Node.js 22.12, Chrome 151, macOS | 61 tests passed, ESM/CJS builds passed, browser bidirectional messaging plus disconnect/reconnect passed |
| Android | [`7134bbd0263fd01d9e7f71b7bd05b226f75b2292`](https://github.com/WuKongIM/WuKongEasySDK-Android/tree/7134bbd0263fd01d9e7f71b7bd05b226f75b2292/example) | JDK 17, Gradle 8.4, Android 14 / API 34 emulator | Gradle tests and Debug build passed; bidirectional messaging, manual disconnect, and heartbeat-timeout semantics passed |
| iOS | [`40014c16c0becd390c105098d359048901f4d87c`](https://github.com/WuKongIM/WuKongEasySDK-iOS/tree/40014c16c0becd390c105098d359048901f4d87c/Examples/WuKongIMExample-Unified) | Xcode 16.2, iPhone 16 / iOS 18.3 Simulator | 30 tests and Release build passed; macOS/iOS scripts passed; bidirectional message content and timestamps rendered correctly |
| Flutter | [`98ab8f3d9a1ad53f40c32caef0979845a37ae9a6`](https://github.com/WuKongIM/WuKongEasySDK-Flutter/tree/98ab8f3d9a1ad53f40c32caef0979845a37ae9a6/example) (`v1.1.0`) | Flutter 3.41.4, Dart 3.11.1, iOS Simulator | 25 tests and static analysis passed; official-example bidirectional messaging passed |

The server also passed its fixed process-level regression:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/easy_sdk_jsonrpc \
  -count=1 -timeout 2m -p=1 -v
```

This result covers a single-node cluster, the default 256 hash slots, JSON-RPC CONNECT, Ping, SEND/SENDACK, RECV/RECVACK, online bidirectional messaging, and client cleanup. It does not cover physical devices, WSS proxies, production-token rejection, offline synchronization, push, multi-device behavior, capacity, or long-duration stability.

#### 1. Start the same WuKongIM revision

Start a development single-node cluster in one terminal:

```bash
git clone https://github.com/WuKongIM/WuKongIM.git
cd WuKongIM
git checkout 5676700d2dc966fa6fc9b2f0620a6ae429adad5a
cp wukongim.toml.example wukongim.toml
go run ./cmd/wukongim -config ./wukongim.toml
```

Check readiness from another terminal:

```bash
curl -fsS http://127.0.0.1:5001/readyz
```

The default development endpoints are Product HTTP at `http://127.0.0.1:5001` and the EasySDK WebSocket at `ws://127.0.0.1:5200`. Production must use protected HTTPS/WSS endpoints.

#### 2. Prepare Alice and Bob

A trusted product backend should prepare each user's `uid`, `token`, and WebSocket URL. Only for a loopback development environment, a trusted terminal can call [`POST /user/token`](https://docs.githubim.com/en/api/product-http/users/setQuickstartUserToken) to create test identities. The commands below assume two native clients; change a user's `device_flag` to `1` when that user signs in through the Web example:

```bash
curl -fsS -X POST http://127.0.0.1:5001/user/token \
  -H 'Content-Type: application/json' \
  -d '{"uid":"alice","token":"alice-token","device_flag":0,"device_level":1}'

curl -fsS -X POST http://127.0.0.1:5001/user/token \
  -H 'Content-Type: application/json' \
  -d '{"uid":"bob","token":"bob-token","device_flag":0,"device_level":1}'
```

Native mobile clients use APP `0`, Web uses WEB `1`, and desktop uses PC `2`. The default product composition exactly validates the stored token for the same UID and device category. A production deployment must still protect this route, implement expiry and rotation policy, and separately prove rejection of invalid, revoked, and product-expired tokens.

Choose the WebSocket URL from where the client runs:

| Client | Local development URL |
| --- | --- |
| Browser, Node.js, macOS app, iOS Simulator | `ws://127.0.0.1:5200` |
| Android Emulator | `ws://10.0.2.2:5200` |
| Physical phone | `ws://<development-host LAN IP>:5200`, with firewall and routing verified |

#### 3. Run a platform example

##### Web

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-JS.git
cd WuKongEasySDK-JS
git checkout 9c03c98c725982fac224cd1d3b52456eae983975
npm ci
npm test
npm run build
python3 -m http.server 8080
```

Open `http://127.0.0.1:8080/example/`. Use two isolated browser contexts for Alice and Bob. Do not print tokens or complete payloads to the Console.

##### Android

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-Android.git
cd WuKongEasySDK-Android
git checkout 61ae6dc6d0077b15e47cda1fd530296b97a06a7a
./gradlew test :example:assembleDebug
./gradlew :example:installDebug
```

Launch the `example` on an API 21+ device or emulator. An Android Emulator reaches the host at `ws://10.0.2.2:5200`, not `localhost`. Use two devices or emulators for two native identities, or use the browser example as the peer.

##### iOS and macOS

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-iOS.git
cd WuKongEasySDK-iOS
git checkout ca688fcac2c4cd8d6f8e8163faf165376b520ba9
swift test
swift build -c release
cd Examples/WuKongIMExample-Unified
./build.sh macos
./build.sh ios
```

Boot an iOS Simulator, then run:

```bash
./build.sh ios --run
```

You can instead start the macOS variant with `./build.sh macos --run`. The iOS ATS cleartext exception in the example exists only for local `ws://` development; do not copy it into a production app.

##### Flutter

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-Flutter.git
cd WuKongEasySDK-Flutter
git checkout 98ab8f3d9a1ad53f40c32caef0979845a37ae9a6
flutter pub get
flutter analyze
flutter test
cd example
flutter pub get
flutter devices
flutter run -d <device-id>
```

Run every target you intend to ship. An iOS Simulator result cannot stand in for Android, Web, or desktop acceptance.

The current Web, Android, and iOS release commits change only version, changelog, or release-documentation metadata above the verified sources; their example behavior source is unchanged. Flutter's release source is the previously verified revision. Use the “Official source examples” table above when reproducing the exact August 31 source receipts.

##### C# / .NET source example

The new [WuKongEasySDK-CSharp](https://github.com/WuKongIM/WuKongEasySDK-CSharp) targets .NET 8 and is released as [WuKongEasySDK 1.0.0](https://www.nuget.org/packages/WuKongEasySDK/1.0.0) on NuGet. It is separate from the four historical released-package receipts above.

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-CSharp.git
cd WuKongEasySDK-CSharp
git checkout d365a354f5e0f25fbd7f83bb59aa365ba43e899f
dotnet build -c Release
dotnet test -c Release
dotnet run --project examples/ConsoleChat -- bob
```

Set `WUKONGIM_WS_URL`, `WUKONGIM_UID`, and `WUKONGIM_TOKEN` first, using Alice and Bob's own credentials in separate terminals. Prepare backend tokens with `device_flag` set to `2`, matching the C# default `DeviceFlag.Desktop`. See the complete [C# quickstart](https://docs.githubim.com/en/sdk/easy/csharp/getting-started).

The C# source passed 35 tests and local NuGet packing on macOS arm64 / .NET `8.0.424`. Against WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d`, a token-authenticated single-node cluster passed bidirectional messaging, reconnect, heartbeat, invalid-token rejection, and cleanup. Reproduce with `WUKONGIM_BINARY=/absolute/path/to/wukongim python3 scripts/smoke.py`; the script owns only its loopback test processes. This is not public NuGet download or production WSS evidence.

See [C# cluster recovery](https://docs.githubim.com/en/sdk/easy/csharp/getting-started#three-node-clusters-and-application-address-replacement) for application endpoint replacement, optional three-node reproduction, and tracked server blockers.

#### 4. Rerun released-package automation

`Safety Automation - EasySDK Released Package Acceptance` resolves the exact four registry versions, builds the current WuKongIM, then uses the released npm package as Bob while Android API 34 and two iOS Simulator jobs complete bidirectional messaging. Trigger a rerun with:

```bash
gh workflow run easysdk-release-acceptance.yml --ref main
```

The workflow checks the actual Gradle, Podfile.lock, and Dart package-config sources so a local source dependency cannot masquerade as a release. Keep real-browser acceptance as a separate Web check.

#### 5. Complete bidirectional acceptance

1. Wait for both clients to report connected before enabling send.
2. Alice sends `{"type":1,"content":"hello bob"}` to person Channel `bob`.
3. Alice observes SEND completion; Bob checks `fromUid`, `channelId`, `messageId`, content, and timestamp.
4. Bob replies to person Channel `alice` to prove the reverse path.
5. Disconnect and reconnect, checking that each transition emits once and listeners do not duplicate messages.
6. Stop the server or cut the network. The client should enter a bounded failure or reconnect state instead of hanging in Connecting.
7. On page or account exit, remove listeners and call the platform's `disconnect`, `dispose`, or `destroy` operation.

Send success, peer realtime receipt, peer display, and product handling are four different states. Design recovery and deduplication separately with [Messaging](https://docs.githubim.com/en/guide/integration/messaging).

#### Troubleshooting

- **The Android emulator cannot connect:** replace `localhost` with `10.0.2.2`; use the development host's LAN address on a physical phone.
- **A checked-out tag behaves differently from the table:** verify that `git rev-parse HEAD` and each package-manager lockfile match the current pins. Do not let a local `path`, `project(':')`, or cached dependency stand in for the registry artifact.
- **SEND succeeds but the peer sees nothing:** verify that the target Channel ID equals the peer UID, both clients are online, and the receiver did not drop RECV through duplicate listeners or incorrect deduplication.
- **An iOS or Android build asks for a License:** accept the applicable Xcode or Android SDK Manager license and download the target runtime, then rerun the same command.
- **A local token connects:** that proves only the built-in exact match. Continue until invalid, revoked, and product-expired tokens are rejected, and define replay limits for still-valid credentials.

#### Next step

Continue with the [iOS](https://docs.githubim.com/en/sdk/easy/ios/getting-started), [Android](https://docs.githubim.com/en/sdk/easy/android/getting-started), [Flutter](https://docs.githubim.com/en/sdk/easy/flutter/getting-started), or [Web](https://docs.githubim.com/en/sdk/easy/javascript/getting-started) quickstart, then use the [Release Checks](https://docs.githubim.com/en/guide/integration/acceptance) to close WSS, physical-device, offline, capacity, and rollback evidence.

#### Rust source example (2026-09-08)

The Rust repository is [WuKongIM/WuKongEasySDK-Rust](https://github.com/WuKongIM/WuKongEasySDK-Rust), with [version `0.1.0` published on crates.io](https://crates.io/crates/wukong-easy-sdk/0.1.0). Applications install `wukong-easy-sdk = "=0.1.0"`; the repository source examples remain available below. Follow the [Rust quickstart](https://docs.githubim.com/en/sdk/easy/rust/getting-started), pin revision `5b4a59cdbb66a9e0c3878e73ba4656f08ee05c6b`, and run `cargo run --locked --example chat` or `cargo run --locked --example roundtrip`. Both Rust identities use PC `2` for `device_flag`.

Verified source `5b4a59cdbb66a9e0c3878e73ba4656f08ee05c6b` passed Linux CI against WuKongIM `27a39f15bf163b433f417b78ab6bfc6e589585e5`, which fixes WebSocket buffer ownership: Rust/Rust checks, 120 seconds of Rust/`easyjssdk@2.0.4` WSS Unicode echoes, three transport interruptions, invalid-Token rejection and cleanup. JS uses WEB `1`; the server is a 256 Hash Slot single-node cluster. The public package separately passed empty-cache download, archive checksum comparison and tutorial compilation; see the [Rust verification record](https://docs.githubim.com/en/sdk/easy/rust/getting-started#verification-record). These source results are separate from the four registry receipts above and do not establish capacity or multi-day stability.

#### C++ prebuilt, vcpkg and source examples

After downloading a [v0.1.0 prebuilt archive](https://github.com/WuKongIM/WuKongEasySDK-CPP/releases/tag/v0.1.0), compile its `example` using the [C++ quickstart](https://docs.githubim.com/en/sdk/easy/cpp/getting-started), without installing vcpkg. Its `wukong_chat` uses the same Token and command-line arguments.

For C++ project `0.1.0`, [vcpkg integration](https://docs.githubim.com/en/sdk/easy/cpp/getting-started) installs the SDK and its dependencies automatically. The source-based interactive example below is retained, separately from the four-platform historical released-package acceptance above. Each terminal supplies its own backend-issued Token through `WKIM_TOKEN`.

```sh
git clone https://github.com/WuKongIM/WuKongEasySDK-CPP.git
cd WuKongEasySDK-CPP
git checkout 3e367a908f42385ab9306f9708b7456399cace7d
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --config Release --parallel 2
ctest --test-dir build -C Release --output-on-failure
./build/wukong_chat ws://127.0.0.1:5200 alice bob
./build/wukong_chat ws://127.0.0.1:5200 bob alice
```

After both connect, send in both directions, observe `SEND completed` and peer `Message`, then enter `/quit`. The default device category is Desktop `2` and must match the backend Token record. The default development listener uses `/`. For dependencies, Windows, private CAs, and thread ownership, see [C++ quickstart](https://docs.githubim.com/en/sdk/easy/cpp/getting-started).

The 2026-09-08 source acceptance used WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d`, Token authentication and 256 hash slots for C++/C++, C++/JS messaging, reconnect, invalid-token rejection, and presence cleanup. For environment and WS/WSS evidence, see [C++ validation](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/3e367a908f42385ab9306f9708b7456399cace7d/docs/VALIDATION.md).

#### Python: run the example with a pinned PyPI package

The Python tutorial uses Python 3.11+ and asyncio with [PyPI `wukong-easy-sdk==0.1.0`](https://pypi.org/project/wukong-easy-sdk/0.1.0/). Follow the [Python quickstart](https://docs.githubim.com/en/sdk/easy/python/getting-started) to create a virtual environment, install the package, and download the `v0.1.0` example source. Set each user's `WKIM_UID`, `WKIM_TOKEN`, `WKIM_PEER`, and `WKIM_URL` in two terminals and run:

```sh
python WuKongEasySDK-Python/examples/chat.py
```

Python defaults to PC/Desktop `2`. Once both display `Connected`, send in both directions and enter `/quit` to exit. The PyPI-installed package run used WuKongIM `0348c0539bbee420a859439695acdac911afa854`, independently of the historical four-platform server revisions above. A Token-authenticated 256-hash-slot single-node cluster passed Python/Python and actual JS `2.0.4` bidirectional messaging, heartbeat, reconnect, invalid-Token rejection, and online cleanup. Reproduce the separate process-level acceptance with:

```sh
python WuKongEasySDK-Python/tests/product.py --server /absolute/path/to/wukongim \
  --js-entry /absolute/path/to/WuKongEasySDK-JS/dist/cjs/index.js
```

The script starts a temporary local cluster, provisions test identities and checks online cleanup through public endpoints, then stops its own processes. Independent protocol tests cover WSS, automatic reconnect, and queue bounds. See the [Python validation record](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/90018a9ac53bdef4cb15dadb169ff835cbfcb2a6/docs/PYPI_VALIDATION.md) for the full scope.

See the [separate acceptance record](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/a9be49e69f4b7155a9a6244ef63f9cb6baf00cb9/docs/CLUSTER_VALIDATION.md) for the subsequent three-node WSS 30-minute run and fault recovery of the same PyPI package; it distinguishes the long run, short CI and their exact versions.

Python also provides a [group example and validation record](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/4d39f9e43265f88415f2fe01fc5418182e405c52/docs/GROUP_VALIDATION.md): four Python/JS clients verify membership changes, isolation, permissions and reconnect over three-node WSS after initial leader convergence. This scenario requires the recorded server recipient-cache fix; the installed PyPI package remains `0.1.0`. See the [Python group quickstart](https://docs.githubim.com/en/sdk/easy/python/getting-started#7-online-group-messaging-and-permissions) for backend setup and CLI commands.


## ios


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/ios/getting-started.mdx)

使用一个应用级客户端依次完成安装、连接、监听、单聊发送和清理。业务后端先提供连接材料，Alice 与 Bob 再用两个独立 App 进程验证双向在线消息。

  当前 Product Gateway 支持固定 `v1.1.1` 的 JSON-RPC CONNECT 与在线双向收发，并兼容对象和 Base64 Payload；camelCase RECVACK 同时携带 `messageId` 与 `messageSeq`，设备线路值为 APP `0`、WEB `1`、PC `2`。教程安装 [CocoaPods 1.1.1](https://cocoapods.org/pods/WuKongEasySDK)；统一 iOS/macOS example 已在源码 `40014c16c0becd390c105098d359048901f4d87c` 上实测通过，该源码已进入 [v1.1.1 Release](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/tag/v1.1.1)。CocoaPods 正式包随后在 iOS Simulator 完成双向消息和断开。

#### 先运行官方 example（推荐）

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-iOS.git
cd WuKongEasySDK-iOS
git checkout 40014c16c0becd390c105098d359048901f4d87c
swift test
swift build -c release
cd Examples/WuKongIMExample-Unified
./build.sh macos
./build.sh ios
```

启动 iOS Simulator 后执行 `./build.sh ios --run`，或用 `./build.sh macos --run` 启动 macOS 版。完整的服务端准备与 Alice/Bob 验收见[运行官方示例](https://docs.githubim.com/zh/sdk/easy/examples)。源码 example 与 iOS `1.1.1` 正式包已经分别留下运行凭据，记录时仍要区分两者。

#### 步骤 5：用 Alice 和 Bob 验收

1. 在两台设备或两个独立 App 进程中分别登录 Alice 与 Bob；
2. 两端都等到 `onConnect` 后再启用发送按钮；
3. Alice 向个人 Channel `bob` 发送消息，保存 `messageId` 与 `messageSeq`；
4. Bob 在 `onMessage` 中核对 `fromUid == "alice"`、Channel 和 Payload；
5. Bob 向 Alice 回发，再验证反向链路；
6. 退出页面或账号，调用 `stop()`，确认没有重复监听和后台连接。

上述精确源码已在 iPhone 16 / iOS 18.3 Simulator 连接同一版 WuKongIM，完成双向消息，并确认正文与时间显示正确；CocoaPods `1.1.1` 正式包随后在本地与托管 iOS Simulator 完成双向消息和断开。你仍需保留服务端与 SDK revision、Podfile.lock、设备和网络环境；物理真机、断网恢复、离线同步和 UI Ready 状态要纳入[上线检查](https://docs.githubim.com/zh/guide/integration/acceptance)。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/ios/getting-started.en.mdx)

Use one application-owned client to install, connect, listen, send a person message, and clean up. The product backend supplies connection material first; Alice and Bob then prove online messaging in two independent app processes.

  The current Product Gateway supports pinned `v1.1.1` JSON-RPC CONNECT and online bidirectional messaging, including object and Base64 payloads; camelCase RECVACK carries both `messageId` and `messageSeq`, and device wire values are APP `0`, WEB `1`, and PC `2`. The tutorial installs [CocoaPods 1.1.1](https://cocoapods.org/pods/WuKongEasySDK). The unified iOS/macOS example passed at source revision `40014c16c0becd390c105098d359048901f4d87c`, which is now included in the [v1.1.1 Release](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/tag/v1.1.1). The CocoaPods artifact then completed bidirectional messaging and disconnect on iOS Simulator.

#### Run the official example first (recommended)

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-iOS.git
cd WuKongEasySDK-iOS
git checkout 40014c16c0becd390c105098d359048901f4d87c
swift test
swift build -c release
cd Examples/WuKongIMExample-Unified
./build.sh macos
./build.sh ios
```

After booting an iOS Simulator, run `./build.sh ios --run`; use `./build.sh macos --run` for macOS. [Run the Official Examples](https://docs.githubim.com/en/sdk/easy/examples) covers server preparation and Alice/Bob acceptance. The source example and iOS `1.1.1` artifact now have separate runtime receipts; keep those evidence classes distinct.

#### Step 5: accept with Alice and Bob

1. Sign in as Alice and Bob on two devices or in two independent app processes.
2. Enable send only after both sides observe `onConnect`.
3. Have Alice send to the person Channel `bob`, retaining `messageId` and `messageSeq`.
4. On Bob, verify `fromUid == "alice"`, the Channel, and the payload in `onMessage`.
5. Send from Bob to Alice and prove the reverse direction.
6. Leave the view or sign out, call `stop()`, and confirm that no duplicate listener or background connection remains.

That exact source ran against the same WuKongIM revision on an iPhone 16 / iOS 18.3 Simulator, completed bidirectional messaging, and rendered content and timestamps correctly. The CocoaPods `1.1.1` artifact then completed bidirectional messaging and disconnect on both local and hosted iOS Simulators. Retain the server and SDK revisions, Podfile.lock, device, and network evidence. Include physical-device execution, network recovery, offline synchronization, and the UI-ready state in your [Release Checks](https://docs.githubim.com/en/guide/integration/acceptance).


## android


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/android/getting-started.mdx)

使用一个进程内单例依次完成安装、初始化、监听、单聊发送和 Activity 清理。Alice 与 Bob 需要运行在两个独立设备、模拟器或应用进程中。

  当前 Product Gateway 支持固定 `v1.0.5` 的 JSON-RPC CONNECT 与在线双向收发，兼容 Android snake_case、驼峰字段、JSON 文本字符串、JSON 对象和 Base64 Payload。教程安装 [Maven Central 1.0.5](https://central.sonatype.com/artifact/com.githubim/easysdk-android/1.0.5)；官方 Android example 已在源码 `7134bbd0263fd01d9e7f71b7bd05b226f75b2292` 上实测通过，该源码已进入 [v1.0.5 Release](https://github.com/WuKongIM/WuKongEasySDK-Android/releases/tag/v1.0.5)。Maven 正式包随后在 Android 14 / API 34 托管模拟器完成双向消息和断开。

#### 先运行官方 example（推荐）

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-Android.git
cd WuKongEasySDK-Android
git checkout 7134bbd0263fd01d9e7f71b7bd05b226f75b2292
./gradlew test :example:assembleDebug
./gradlew :example:installDebug
```

Android Emulator 填写 `ws://10.0.2.2:5200`，不能填写 `localhost`。完整的服务端准备与 Alice/Bob 验收见[运行官方示例](https://docs.githubim.com/zh/sdk/easy/examples)。源码 example 与 Maven `1.0.5` 正式包已经分别留下运行凭据，记录时仍要区分两者。

#### 步骤 6：用 Alice 和 Bob 验收

由于 SDK 是进程内单例，请使用两台设备、两个模拟器或两个独立应用进程：

1. Alice 与 Bob 分别从业务后端取得自己的连接材料；
2. 两端都观察 `CONNECT`，失败时只记录稳定错误码与阶段，不记录原始错误文本；
3. Alice 调用 `sendText("bob", ...)`，记录发送结果；
4. Bob 核对 `fromUid`、`channelId`、`messageId` 和 Payload；
5. Bob 向 Alice 回发，验证反向链路；
6. 重建 Activity，确认监听器没有重复；退出应用时确认连接已关闭。

上述精确源码已在 Android 14 / API 34 模拟器连接同一版 WuKongIM，完成双向消息、手动断开和心跳超时验证；Maven `1.0.5` 正式包随后在托管 API 34 模拟器通过 instrumentation 双向消息与断开。仍应保留服务端 revision、SDK revision、包解析结果、设备与网络环境，不要用盲目重试掩盖失败；物理真机、日志脱敏、离线恢复与生产安全需要另外验收。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/android/getting-started.en.mdx)

Use one process singleton to install, initialize, listen, send a person message, and clean up with the Activity lifecycle. Alice and Bob run in two independent devices, emulators, or application processes.

  The current Product Gateway supports pinned `v1.0.5` JSON-RPC CONNECT and online bidirectional messaging, including Android snake_case, camel-case fields, JSON text strings, JSON objects, and Base64 payloads. The tutorial installs [Maven Central 1.0.5](https://central.sonatype.com/artifact/com.githubim/easysdk-android/1.0.5). The official Android example passed at source revision `7134bbd0263fd01d9e7f71b7bd05b226f75b2292`, which is now included in the [v1.0.5 Release](https://github.com/WuKongIM/WuKongEasySDK-Android/releases/tag/v1.0.5). The Maven artifact then completed bidirectional messaging and disconnect on a hosted Android 14 / API 34 emulator.

#### Run the official example first (recommended)

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-Android.git
cd WuKongEasySDK-Android
git checkout 7134bbd0263fd01d9e7f71b7bd05b226f75b2292
./gradlew test :example:assembleDebug
./gradlew :example:installDebug
```

Use `ws://10.0.2.2:5200` from an Android Emulator, not `localhost`. [Run the Official Examples](https://docs.githubim.com/en/sdk/easy/examples) covers server preparation and Alice/Bob acceptance. The source example and Maven `1.0.5` artifact now have separate runtime receipts; keep those evidence classes distinct.

#### Step 6: accept with Alice and Bob

Because the SDK is a process singleton, use two devices, two emulators, or two independent application processes:

1. Alice and Bob each obtain their own connection material from the product backend.
2. Both sides observe `CONNECT`; on failure, record only a stable error code and stage, never the original error text.
3. Alice calls `sendText("bob", ...)` and records the send result.
4. Bob checks `fromUid`, `channelId`, `messageId`, and the payload.
5. Bob sends back to Alice and proves the reverse direction.
6. Recreate the Activity and confirm listeners are not duplicated; exit the application and confirm the connection closes.

That exact source ran against the same WuKongIM revision on an Android 14 / API 34 emulator and completed bidirectional messaging, manual disconnect, and heartbeat-timeout verification. The Maven `1.0.5` artifact then passed instrumentation bidirectional messaging and disconnect on a hosted API 34 emulator. Retain the server revision, SDK revision, package-resolution result, device, and network environment, and do not hide failures with blind retries. Physical-device execution, log redaction, offline recovery, and production security require separate acceptance.


## flutter


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/flutter/getting-started.mdx)

使用一个 `StatefulWidget` 依次完成安装、初始化、监听、单聊发送和 `dispose` 清理。Alice 与 Bob 运行在两个独立设备、模拟器或浏览器上下文中。

  当前 Product Gateway 支持固定 `v1.1.0` 的 JSON-RPC CONNECT 与在线双向收发：客户端以 Base64 发送，服务端对有效 JSON 输出对象 RECV。设备线路值为 APP `0`、WEB `1`、PC `2`。教程使用 [pub.dev 1.1.0](https://pub.dev/packages/wukong_easy_sdk/versions/1.1.0) 与对应 [Release](https://github.com/WuKongIM/WuKongEasySDK-Flutter/releases/tag/v1.1.0)；同一 revision `98ab8f3d9a1ad53f40c32caef0979845a37ae9a6` 的官方 example 与从 pub.dev 解析的正式包均已在 iOS Simulator 完成双向消息和断开。其他 Flutter 目标、WSS 与生产 Token 校验仍须独立验收。

#### 先运行官方 example（推荐）

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-Flutter.git
cd WuKongEasySDK-Flutter
git checkout 98ab8f3d9a1ad53f40c32caef0979845a37ae9a6
flutter pub get
flutter analyze
flutter test
cd example
flutter pub get
flutter run -d <device-id>
```

这个 revision 就是正式 `v1.1.0`；源码 example 和 hosted 正式包的完整服务端准备、设备地址及 Alice/Bob 验收见[运行官方示例](https://docs.githubim.com/zh/sdk/easy/examples)。

#### 步骤 5：用 Alice 和 Bob 验收

1. 在两台设备、两个模拟器或两个独立浏览器上下文中分别启动 Alice 与 Bob；
2. 两端都等到 `WuKongEvent.connect` 后再启用发送；
3. Alice 向个人 Channel `bob` 发送消息，记录 ID、序号和 Reason Code；
4. Bob 核对 `fromUid`、`channelId`，检查对象 Payload 的 `type` 与 `content`，并按 `messageId` 去重；
5. Bob 向 Alice 回发，验证反向链路；
6. 销毁并重新打开页面，确认没有重复监听，再验证退出账号后的资源释放。

上述正式 `v1.1.0` example 已在 iOS Simulator 连接同一版 WuKongIM，完成双向消息；25 个测试和静态分析也通过。从 pub.dev 解析且 package config 标记为 `hosted` 的同版本产物随后在托管 iOS Simulator 再次完成双向消息和断开。该闭环没有证明 Android、Web、桌面目标、应用后台恢复、离线消息同步或多端会话一致性，继续按[消息收发](https://docs.githubim.com/zh/guide/integration/messaging)和[上线检查](https://docs.githubim.com/zh/guide/integration/acceptance)补齐。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/flutter/getting-started.en.mdx)

Use one `StatefulWidget` to install, initialize, listen, send a person message, and clean up in `dispose`. Alice and Bob run in two independent devices, simulators, or browser contexts.

  The current Product Gateway supports pinned `v1.1.0` JSON-RPC CONNECT and online bidirectional messaging: the client sends Base64, while the server emits object RECV for valid JSON. Device wire values are APP `0`, WEB `1`, and PC `2`. The tutorial uses [pub.dev 1.1.0](https://pub.dev/packages/wukong_easy_sdk/versions/1.1.0) and its matching [Release](https://github.com/WuKongIM/WuKongEasySDK-Flutter/releases/tag/v1.1.0). The official example at the same revision, `98ab8f3d9a1ad53f40c32caef0979845a37ae9a6`, and the artifact resolved from pub.dev both completed bidirectional messaging and disconnect on iOS Simulator. Every other Flutter target, WSS, and production-token path still needs independent evidence.

#### Run the official example first (recommended)

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-Flutter.git
cd WuKongEasySDK-Flutter
git checkout 98ab8f3d9a1ad53f40c32caef0979845a37ae9a6
flutter pub get
flutter analyze
flutter test
cd example
flutter pub get
flutter run -d <device-id>
```

This revision is the released `v1.1.0`. [Run the Official Examples](https://docs.githubim.com/en/sdk/easy/examples) covers server preparation, device addressing, and Alice/Bob acceptance for both the source example and hosted artifact.

#### Step 5: accept with Alice and Bob

1. Start Alice and Bob on two devices, two simulators, or two independent browser contexts.
2. Enable send only after both sides observe `WuKongEvent.connect`.
3. Have Alice send to the person Channel `bob`, recording ID, sequence, and Reason Code.
4. On Bob, verify `fromUid` and `channelId`, inspect `type` and `content` in the object payload, and deduplicate by `messageId`.
5. Have Bob send to Alice and prove the reverse direction.
6. Destroy and reopen the page, confirm no duplicate listener remains, then verify final resource release on logout.

The released `v1.1.0` example ran against the same WuKongIM revision on an iOS Simulator and completed bidirectional messaging; all 25 tests and static analysis also passed. The same version resolved from pub.dev with a `hosted` package-config source then completed bidirectional messaging and disconnect again on a hosted iOS Simulator. This does not prove Android, Web, desktop, application-background recovery, offline message synchronization, or multi-device conversation consistency. Continue with [Messaging](https://docs.githubim.com/en/guide/integration/messaging) and the [Release Checks](https://docs.githubim.com/en/guide/integration/acceptance).


## javascript


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/javascript/getting-started.mdx)

浏览器先向业务 BFF 请求当前用户的连接材料，再由 EasyJSSDK 建立 WebSocket JSON-RPC 连接。客户端不持有 Product HTTP 管理凭据，Alice 与 Bob 使用两个隔离的浏览器上下文验收。

  当前 Product Gateway 支持固定 `v2.0.4` 的 JSON-RPC CONNECT 与在线双向收发，包括省略 `jsonrpc`、Base64 SEND 与对象 RECV。浏览器默认使用 WEB `1`，完整线路值为 APP `0`、WEB `1`、PC `2`。教程安装 [npm 2.0.4](https://www.npmjs.com/package/easyjssdk/v/2.0.4)；官方浏览器 example 已在源码 `a055b3667247333b6b3183249f5d5929673dfd53` 上实测通过，该源码已进入 [v2.0.4 Release](https://github.com/WuKongIM/WuKongEasySDK-JS/releases/tag/v2.0.4)。npm 正式包随后在 Chrome 151 与托管正式包对端中完成双向消息和断开。

#### 先运行官方 example（推荐）

要复现已验证路径，检出精确源码并启动仓库自带页面：

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-JS.git
cd WuKongEasySDK-JS
git checkout a055b3667247333b6b3183249f5d5929673dfd53
npm ci
npm test
npm run build
python3 -m http.server 8080
```

打开 `http://127.0.0.1:8080/example/`。完整的 WuKongIM 启动、Alice/Bob 准备和地址映射见[运行官方示例](https://docs.githubim.com/zh/sdk/easy/examples)。源码 example 与 npm `2.0.4` 正式包已经分别留下运行凭据，记录时仍要区分两者。

#### 步骤 5：用 Alice 和 Bob 验收

1. 打开两个独立浏览器 Profile 或两个隔离的浏览器上下文，分别登录 Alice 与 Bob；
2. 两端都从各自的产品 Session 获取连接材料并观察 `WKIMEvent.Connect`；
3. Alice 向个人 Channel `bob` 发送消息，记录发送结果；
4. Bob 核对 `fromUid`、`channelId`、`messageId` 和 Payload；
5. Bob 向 Alice 回发，验证反向链路；
6. 模拟不可达地址，确认 10 秒内失败并 `destroy`，页面不会永久停在 Connecting；
7. 刷新、关闭或退出账号，确认旧实例已 `off` 并 `destroy`，没有重复连接。

上述精确源码已在 Chrome 151 中连接同一版 WuKongIM，完成双向消息、主动断开和重连；npm `2.0.4` 正式包也在真实 Chrome 与托管正式包对端中完成双向消息、SENDACK 和断开。该闭环仍没有覆盖离线同步或生产 WSS；如果 Bob 离线，应用不能仅等待 EasySDK 的实时事件，必须用产品设计的持久同步路径恢复消息。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/javascript/getting-started.en.mdx)

The browser asks the product BFF for the current user's connection material, then EasyJSSDK establishes a WebSocket JSON-RPC connection. The client holds no Product HTTP management credential, and Alice and Bob accept the path in two isolated browser contexts.

  The current Product Gateway supports pinned `v2.0.4` JSON-RPC CONNECT and online bidirectional messaging, including requests without `jsonrpc`, Base64 SEND, and object RECV. Browsers default to WEB `1`; the full wire set is APP `0`, WEB `1`, and PC `2`. The tutorial installs [npm 2.0.4](https://www.npmjs.com/package/easyjssdk/v/2.0.4). The official browser example passed at source revision `a055b3667247333b6b3183249f5d5929673dfd53`, which is now included in the [v2.0.4 Release](https://github.com/WuKongIM/WuKongEasySDK-JS/releases/tag/v2.0.4). The npm artifact then completed bidirectional messaging and disconnect in Chrome 151 and hosted released-package peer runs.

#### Run the official example first (recommended)

Check out the exact source to reproduce the verified path:

```bash
git clone https://github.com/WuKongIM/WuKongEasySDK-JS.git
cd WuKongEasySDK-JS
git checkout a055b3667247333b6b3183249f5d5929673dfd53
npm ci
npm test
npm run build
python3 -m http.server 8080
```

Open `http://127.0.0.1:8080/example/`. [Run the Official Examples](https://docs.githubim.com/en/sdk/easy/examples) covers WuKongIM startup, Alice/Bob preparation, and endpoint mapping. The source example and npm `2.0.4` artifact now have separate runtime receipts; keep those evidence classes distinct.

#### Step 5: accept with Alice and Bob

1. Open two independent browser profiles or isolated browser contexts and sign in as Alice and Bob.
2. Both sides obtain connection material from their own product session and observe `WKIMEvent.Connect`.
3. Have Alice send to the person Channel `bob`, retaining the send result.
4. On Bob, verify `fromUid`, `channelId`, `messageId`, and payload.
5. Have Bob send back to Alice and prove the reverse direction.
6. Use an unreachable address and confirm failure plus `destroy` within 10 seconds, rather than leaving the page in Connecting forever.
7. Refresh, close, or sign out and confirm that the old instance called `off` and `destroy`, leaving no duplicate connection.

That exact source ran against the same WuKongIM revision in Chrome 151 and completed bidirectional messaging, manual disconnect, and reconnect. The npm `2.0.4` artifact also completed bidirectional messaging, SENDACK, and disconnect in real Chrome and hosted released-package peer runs. The loop still does not cover offline synchronization or production WSS. When Bob is offline, the application cannot wait only for an EasySDK realtime event; it needs a product-designed durable synchronization path.


## csharp


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/csharp/getting-started.mdx)

[WuKongEasySDK-CSharp](https://github.com/WuKongIM/WuKongEasySDK-CSharp) 参考 JavaScript EasySDK 的 WebSocket JSON-RPC 协议，提供符合 C# 习惯的异步 API 和强类型事件。运行时不依赖第三方包。

  [WuKongEasySDK 1.0.0](https://www.nuget.org/packages/WuKongEasySDK/1.0.0) 已发布到 nuget.org，对应源码 `02ea7d60cd94feef1996f41bca35ffc3b8e18ea6`。下文默认使用公共 NuGet 安装；源码引用与本地打包用于需要自行构建的场景。

#### 6. 运行官方示例和验收

在固定源码目录执行：

```bash
dotnet build -c Release
dotnet test -c Release
dotnet run --project examples/ConsoleChat -- bob
```

控制台示例使用同样的环境变量，输入文本发送，`/quit` 或 Ctrl+C 退出。它把正文显示为聊天界面内容，不输出完整协议对象。

已有服务端二进制时，还可运行自动化真实进程测试：

```bash
WUKONGIM_BINARY=/absolute/path/to/wukongim python3 scripts/smoke.py
```

该脚本只启动和清理自己拥有的回环地址单节点集群，使用 256 Hash Slots 并启用 Token 认证。它自动准备两个测试账号，检查双向 Unicode 消息的 SENDACK/RECV 一致性、重连、心跳和错误 Token 拒绝。

原始实现 `d365a354f5e0f25fbd7f83bb59aa365ba43e899f` 在 macOS arm64、.NET SDK `8.0.424` / runtime `8.0.30` 通过 35 项测试、Release 构建与本地 NuGet 打包，并对 WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d` 完成上述真实进程验证。自定义事件与故障重连由回环 WebSocket 测试覆盖。此记录不包含公共 NuGet 下载、生产 WSS、离线恢复、多节点容量或长期稳定性验证。

NuGet `1.0.0` 的[独立发布验证](https://github.com/WuKongIM/WuKongEasySDK-CSharp/actions/runs/34188740990)覆盖 Windows、Linux、macOS 构建、35 项 SDK 测试、7 项发布校验测试与全新项目本地包安装。发布后从 nuget.org 下载精确版本，比较除 NuGet 签名外的全部包内容与测试产物，并在空包缓存、仅公共源的独立项目中完成还原、编译、加载和释放。此公共安装记录与上述真实服务端通信记录分别保留。

##### C# / JavaScript 真实互通

早期独立的 [C#/JS 互通 CI](https://github.com/WuKongIM/WuKongEasySDK-CSharp/actions/runs/34191886039) 分别测试公共 NuGet `WuKongEasySDK 1.0.0`（空包缓存安装）与当前 C# 源码。固定对端为 npm `easyjssdk 2.0.4`、Node 24 + `ws 8.21.3`，服务端为 `v3.0.0-beta.9` / `734166e0ec30fc0f6f10fef6f6d1889d079ab636`。测试工具源码为 `1db387c4f794a45bdb6f4e419d68dbe2bfef740f`，与 NuGet 包源码分别记录。

五组场景覆盖双向中文/emoji 与嵌套自定义 Payload、超出 JavaScript 安全整数范围的消息 ID 及 SENDACK/RECV 一致性、错误 Token 拒绝、断网后自动重连、真实服务端崩溃重启，以及主动断开后拒绝离线 SEND 并允许显式重连。该服务端的 SENDACK 不返回 `clientMsgNo`，测试会校验 RECV 保留原始关联号。

上述[早期复现记录](https://github.com/WuKongIM/WuKongEasySDK-CSharp/blob/1db387c4f794a45bdb6f4e419d68dbe2bfef740f/docs/interoperability.md)的范围为 Node + `ws`。其中发现的 Node 原生 WebSocket 重连停滞，已在下面的独立源码修复与验收中处理。

##### Chromium/WSS 与原生 Node 恢复

[早期扩展互通 CI](https://github.com/WuKongIM/WuKongEasySDK-CSharp/actions/runs/34195913516)分别使用正式 NuGet `1.0.0` 和 C# 候选源码，验证 Node `24.3.0` 原生 WebSocket，以及 Playwright `1.62.1` 驱动的真实 Chromium。服务端仍固定为 `734166e0ec30fc0f6f10fef6f6d1889d079ab636`，JS 固定为修复源码 `5e5dfb727fb0ea08294939962ae799e998b7ca5c`。该修复结束只发出 `error`、没有后续 `close` 的握手失败，让有限次数重连继续执行；npm `easyjssdk 2.0.4` **不包含此修复**。

浏览器从真实 HTTPS 页面建立 WSS 连接，C# 使用原有 `ClientWebSocket`。测试通过临时 CA 和隔离的浏览器 NSS 信任库保留正常证书校验，要求双方拒绝不受信任的 CA 和主机名不匹配的证书，且没有解密后的应用请求进入服务端协议。证书控制通过后，再执行双向消息、错误 Token、断网恢复、服务端重启和主动断开五组场景。

JS 修复已随 [npm `easyjssdk 2.0.5`](https://www.npmjs.com/package/easyjssdk/v/2.0.5) 正式发布，对应源码 `b6d0bbe822b9c5b6f95a10d55b593d30184414f6`。[公共包互通 CI](https://github.com/WuKongIM/WuKongEasySDK-CSharp/actions/runs/34198683155) 从空缓存安装该固定 npm 包，六组正式 NuGet / C# 候选源码与 `ws`、原生 Node、Chromium WSS 的组合均通过，包含上述证书拒绝控制。

该单节点矩阵运行四条 Linux 任务，产生六份报告，记录 npm 下载地址及 SHA-512、实际测试提交、Chromium 版本和证书控制结果；[复现与信任范围](https://github.com/WuKongIM/WuKongEasySDK-CSharp/blob/c143be832d6439295a213176274faf2ffb4b9868/docs/interoperability.md)说明运行方法。这里验证的是临时 CA、回环 TLS 代理和 Chromium，不代表所有公网 CA、反向代理、Firefox/WebKit、多节点容量或长期稳定性。

##### 三节点集群与应用切换地址

完整三节点故障恢复验收尚未通过，服务端迁移、恢复日志冲突和重启后漏收问题已记录在 [Issue #927](https://github.com/WuKongIM/WuKongIM/issues/927)。本轮只处理 SDK、测试和文档；此前未合并的服务端实验不能作为已发布能力依据。上文已通过的单节点 WS／WSS 验证仍保留各自的精确版本和运行记录。

**地址切换由应用负责。** 一个 SDK 实例只有一个固定地址，自动重连仍访问该地址。业务后端选择存活入口并取得其 `/route`，客户端等待旧实例 `DisposeAsync` 完成，再使用新地址与原有凭据创建实例、注册事件并连接。中断中的 SEND 可能结果未知，不应自动重发已经确认或结果不明的消息。

[三节点手动复现工具](https://github.com/WuKongIM/WuKongEasySDK-CSharp/blob/8a1525dce74ad23cf8f7f47d91adaa9fc5b459f1/docs/cluster-acceptance.md)固定公共 NuGet `WuKongEasySDK 1.0.0`、npm `easyjssdk 2.0.5`、Node `24.3.0`，服务端使用已发布的 `v3.0.0-beta.9` 源码 `734166e0ec30fc0f6f10fef6f6d1889d079ab636`。三个回环进程使用 256 Hash Slots、10 个逻辑 Slot 和三副本；测试覆盖入口间单聊/群聊、断线与离线发送拒绝、应用替换实例、节点重新加入，并严格检查 ACK/RECV 的关联和消息内容。节点上线、ISR 元数据齐全不等于物理副本已经追平。

常规 push/PR CI 运行四条单节点任务，产生六份 WS／原生 Node／Chromium WSS 报告。手动触发时选择 `include_cluster=true` 才追加两条三节点复现任务；遇到服务端阻塞会正常失败，不计为 SDK 三节点验收通过。本地运行命令为 `python3 scripts/interop.py --transport native --topology three-node`，候选源码增加 `--candidate`，并按工具说明提供精确服务端二进制。

复现工具记录服务端默认 90 秒在线路由租期引起的登录延迟，以及显式的迁移扫描预算。对已观察到的登录错误 `15` 有限重试是复现控制，不是重试所有系统错误或 SEND 的建议。完整三节点恢复、立即切换、多节点 WSS、离线同步、大群容量与长期稳定性均未由该工具建立保证。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/csharp/getting-started.en.mdx)

[WuKongEasySDK-CSharp](https://github.com/WuKongIM/WuKongEasySDK-CSharp) follows the JavaScript EasySDK WebSocket JSON-RPC protocol with idiomatic async C# APIs and typed events. It has no third-party runtime dependencies.

  [WuKongEasySDK 1.0.0](https://www.nuget.org/packages/WuKongEasySDK/1.0.0) is published on nuget.org from source `02ea7d60cd94feef1996f41bca35ffc3b8e18ea6`. The default installation below uses public NuGet; project references and local packing remain available for source builds.

#### 6. Run the official example and acceptance

From the pinned checkout:

```bash
dotnet build -c Release
dotnet test -c Release
dotnet run --project examples/ConsoleChat -- bob
```

The console example uses the same environment variables. Type text to send, and use `/quit` or Ctrl+C to exit. It displays message text as chat UI without printing complete protocol objects.

With a prebuilt server binary, run the automated real-process fixture:

```bash
WUKONGIM_BINARY=/absolute/path/to/wukongim python3 scripts/smoke.py
```

The script starts and cleans up only its own loopback single-node cluster with 256 hash slots and Token authentication. It prepares two test identities and verifies matching SENDACK/RECV for bidirectional Unicode messages, reconnect, heartbeat, and invalid-token rejection.

The original implementation `d365a354f5e0f25fbd7f83bb59aa365ba43e899f` passed 35 tests, Release builds, and local NuGet packing on macOS arm64 with .NET SDK `8.0.424` / runtime `8.0.30`. It also passed the real-process fixture against WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d`. Loopback WebSocket tests cover custom events and reconnect failures. This receipt excludes public NuGet downloads, production WSS, offline recovery, multi-node capacity, and long-duration stability.

The independent [NuGet `1.0.0` release verification](https://github.com/WuKongIM/WuKongEasySDK-CSharp/actions/runs/34188740990) covers builds, 35 SDK tests, seven release guard tests, and clean local package installation on Windows, Linux, and macOS. After publication it downloads the exact version from nuget.org, compares every entry except NuGet’s signature with the tested artifact, and restores, compiles, loads, and disposes the client in an isolated project with an empty package cache and only the public feed. This public installation receipt is separate from the real-server messaging receipt above.

##### C# / JavaScript interoperability

The earlier independent [C#/JS interoperability CI](https://github.com/WuKongIM/WuKongEasySDK-CSharp/actions/runs/34191886039) tests both public NuGet `WuKongEasySDK 1.0.0` restored into an empty package cache and candidate C# source. It pins npm `easyjssdk 2.0.4`, Node 24 with `ws 8.21.3`, and server `v3.0.0-beta.9` / `734166e0ec30fc0f6f10fef6f6d1889d079ab636`. Harness source `1db387c4f794a45bdb6f4e419d68dbe2bfef740f` is recorded separately from the NuGet package source.

Five scenario groups cover bidirectional Chinese/emoji and nested custom payloads, exact message IDs above JavaScript's safe integer range and matching SENDACK/RECV, invalid Token rejection, automatic network recovery, real server crash/restart, and manual disconnect with rejected offline SEND followed by explicit reconnect. This server omits `clientMsgNo` in SENDACK; RECV must preserve the supplied correlation value.

The [earlier reproduction record](https://github.com/WuKongIM/WuKongEasySDK-CSharp/blob/1db387c4f794a45bdb6f4e419d68dbe2bfef740f/docs/interoperability.md) covers Node with `ws`. The native Node reconnect stall observed there is addressed by the separately identified source repair and acceptance below.

##### Chromium/WSS and native Node recovery

The [earlier extended interoperability CI](https://github.com/WuKongIM/WuKongEasySDK-CSharp/actions/runs/34195913516) tests both public NuGet `1.0.0` and candidate C# source with Node `24.3.0` native WebSocket and real Chromium driven by Playwright `1.62.1`. It retains server pin `734166e0ec30fc0f6f10fef6f6d1889d079ab636` and pins repaired JS source `5e5dfb727fb0ea08294939962ae799e998b7ca5c`. The repair settles handshake failures that emit `error` without a subsequent `close`, allowing bounded retries to continue. npm `easyjssdk 2.0.4` **does not contain this repair**.

The browser opens a real HTTPS page and connects over WSS; C# uses its existing `ClientWebSocket`. A temporary CA and isolated browser NSS trust database preserve normal certificate validation. Both clients must reject untrusted CA and mismatched-host certificates without forwarding decrypted application requests to the server protocol. These controls precede the five bidirectional messaging, invalid Token, network recovery, server restart, and manual disconnect scenarios.

The JS repair is now published in [npm `easyjssdk 2.0.5`](https://www.npmjs.com/package/easyjssdk/v/2.0.5), from source `b6d0bbe822b9c5b6f95a10d55b593d30184414f6`. The [public-package interoperability CI](https://github.com/WuKongIM/WuKongEasySDK-CSharp/actions/runs/34198683155) installs that exact npm package into an empty cache. All six combinations of released NuGet / candidate C# with `ws`, native Node, and Chromium WSS passed, including the certificate rejection controls above.

That single-node matrix runs four Linux jobs, producing six reports that retain the npm tarball URL and SHA-512 integrity, actual harness commit, Chromium version, and certificate controls. See [reproduction and trust scope](https://github.com/WuKongIM/WuKongEasySDK-CSharp/blob/c143be832d6439295a213176274faf2ffb4b9868/docs/interoperability.md). This verifies the temporary CA, loopback TLS proxy, and Chromium; it does not establish every public CA, reverse proxy, Firefox/WebKit, multi-node capacity, or long-duration stability.

##### Three-node clusters and application address replacement

Complete three-node fault recovery acceptance has not passed. Server migration, recovery log conflicts, and post-rejoin delivery findings are tracked in [issue 927](https://github.com/WuKongIM/WuKongIM/issues/927). This task covers SDK work, tests, and documentation; earlier unmerged server experiments are not released capability evidence. The passing single-node WS/WSS results above retain their own exact versions and run records.

**The application owns address replacement.** Each SDK instance retries its fixed endpoint. A trusted backend selects a live ingress and obtains its `/route`. Await `DisposeAsync` on the previous instance, then create a replacement with the new URL and existing credentials, register handlers, and connect. An interrupted SEND may have an unknown outcome; do not automatically replay acknowledged or ambiguous messages.

The [manual three-node reproduction](https://github.com/WuKongIM/WuKongEasySDK-CSharp/blob/8a1525dce74ad23cf8f7f47d91adaa9fc5b459f1/docs/cluster-acceptance.md) pins public NuGet `WuKongEasySDK 1.0.0`, npm `easyjssdk 2.0.5`, Node `24.3.0`, and released server `v3.0.0-beta.9` source `734166e0ec30fc0f6f10fef6f6d1889d079ab636`. Three loopback processes use 256 hash slots, 10 logical Slots, and three replicas. The fixture checks cross-ingress person/group messages, disconnect and offline-send rejection, application instance replacement, and rejoin with strict ACK/RECV correlation and payload checks. Node readiness and full ISR metadata do not prove physical replica catch-up.

Regular push/PR CI runs four single-node jobs and produces six WS/native Node/Chromium WSS reports. Manual dispatch with `include_cluster=true` adds two cluster reproduction jobs. Server blockers fail those jobs normally and do not count as passing SDK cluster acceptance. Run locally with `python3 scripts/interop.py --transport native --topology three-node`, adding `--candidate` for C# source and supplying the exact server binary as documented by the fixture.

The reproduction records login delays under the default 90-second presence lease and its explicit migration scan budget. Bounded retry of the observed activation error `15` is a reproduction control, not advice to retry arbitrary system errors or SENDs. It does not establish complete three-node recovery, immediate address switching, multi-node WSS, offline synchronization, large-group capacity, or long-duration stability.


## cpp


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/cpp/getting-started.mdx)

[WuKongEasySDK-CPP](https://github.com/WuKongIM/WuKongEasySDK-CPP) 参考 JS `v2.0.4` 实现 WebSocket JSON-RPC CONNECT、在线收发、自动 RECVACK、心跳、重连和自定义事件。使用 C++17、Boost.Beast、OpenSSL 与 nlohmann/json。

  本文固定工程 `0.1.0` 的源码 `3e367a908f42385ab9306f9708b7456399cace7d`，支持本仓库维护的 vcpkg Git registry 、CMake 源码安装及 v0.1.0 预编译 SDK 压缩包；该 registry 不属于微软默认目录。该源码已连接 WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d`，在开启 Token 鉴权的 256 hash slots 单节点集群中完成 C++/C++ 和 C++/JS 双向消息。完整范围见仓库的 [验证记录](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/3e367a908f42385ab9306f9708b7456399cace7d/docs/VALIDATION.md)。

#### 2. 推荐：vcpkg + CMake

先安装 [vcpkg](https://learn.microsoft.com/zh-cn/vcpkg/get_started/get-started)，
将 `VCPKG_ROOT` 指向安装目录。准备 Git、CMake 3.20+ 和 C++17 编译器：
Windows 使用 Visual Studio 2022，macOS 使用 Xcode 命令行工具，Linux 使用 GCC/Clang。
验证使用的 vcpkg 工具版本为 `04a9d8e5212d01ee1dd9478eadd9caade4f8b0d4`。

在你的应用目录创建 `vcpkg.json`：

```json
{"dependencies": ["wukong-easy-sdk"]}
```

`vcpkg-configuration.json`:

```json
{
  "default-registry": {
    "kind": "git",
    "repository": "https://github.com/microsoft/vcpkg",
    "baseline": "04a9d8e5212d01ee1dd9478eadd9caade4f8b0d4"
  },
  "registries": [
    {
      "kind": "git",
      "repository": "https://github.com/WuKongIM/WuKongEasySDK-CPP.git",
      "baseline": "63ec99d34c7605b64e2173d201639042e0e49de9",
      "packages": [
        "wukong-easy-sdk"
      ]
    }
  ]
}
```

在自己的 `main.cpp` 旁添加 `CMakeLists.txt`：

```cmake
cmake_minimum_required(VERSION 3.20)
project(my_app LANGUAGES CXX)
find_package(WuKongEasySDK 0.1 CONFIG REQUIRED)
add_executable(my_app main.cpp)
target_link_libraries(my_app PRIVATE WuKongEasySDK::WuKongEasySDK)
```
```sh
# Linux / macOS
cmake -S . -B build -DCMAKE_TOOLCHAIN_FILE="$VCPKG_ROOT/scripts/buildsystems/vcpkg.cmake" -DCMAKE_BUILD_TYPE=Release
cmake --build build --config Release --parallel 2
```

```powershell
# Windows / Visual Studio 2022
cmake -S . -B build -DCMAKE_TOOLCHAIN_FILE="$env:VCPKG_ROOT/scripts/buildsystems/vcpkg.cmake" -DVCPKG_TARGET_TRIPLET=x64-windows
cmake --build build --config Release --parallel 2
```

vcpkg 会自动安装 SDK、Boost、OpenSSL 和 JSON，无需手动逐个安装。
首次构建可能需要编译依赖并耗时数分钟，这不是预编译压缩包。
SDK port 为静态库；Windows 使用 `x64-windows` 时，分发应用需要携带构建时复制到程序旁的依赖 DLL。

这是 WuKongIM 在本仓库维护的公开 Git registry，不是微软默认目录中的包，
因此必须同时提供 registry 配置和依赖声明。SDK 源码固定为
`3e367a908f42385ab9306f9708b7456399cace7d`，与 registry baseline 分别固定。
将两个 JSON 文件提交到应用仓库；已有 manifest 的项目应合并字段，不要覆盖原有依赖。

截至 2026-09-08，微软默认目录的收录申请已提交到 [microsoft/vcpkg#53837](https://github.com/microsoft/vcpkg/pull/53837)，尚未合并。申请中的 port 从正式 `v0.1.0` 标签构建；上面的安装方式仍使用 WuKongIM 自定义 registry，当前不能省略 `vcpkg-configuration.json`。

[独立消费端示例](https://github.com/WuKongIM/WuKongEasySDK-CPP/tree/main/examples/vcpkg-consumer) · [Registry 维护说明](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/main/docs/VCPKG.md)

#### 备选：下载预编译包，解压接入

希望跳过依赖编译时，从 [C++ SDK v0.1.0 Release](https://github.com/WuKongIM/WuKongEasySDK-CPP/releases/tag/v0.1.0) 下载匹配的 ZIP 和 `SHA256SUMS`，验证 SHA-256 后解压。只需准备 CMake 3.20+ 和匹配的 C++ 开发环境，无需另装 vcpkg。

| 压缩包后缀 | 对应环境 |
| --- | --- |
| `linux-x64-gcc13.zip` | Ubuntu 24.04 x64、GCC 13、libstdc++ C++11 ABI、glibc 2.39+ |
| `macos-arm64-appleclang.zip` | macOS 14+ arm64、Apple Clang、libc++ |
| `windows-x64-msvc143-md.zip` | Windows x64、Visual Studio 2022 v143；Release `/MD`、Debug `/MDd` |

包名统一以 `WuKongEasySDK-CPP-0.1.0-` 开头。包内包含 Debug/Release 静态 SDK、Boost/JSON 头文件、OpenSSL 库、许可文件和最小示例。在解压目录执行：

```sh
# Linux / macOS
cmake -S example -B build -DCMAKE_TOOLCHAIN_FILE="$PWD/wukong-sdk.cmake" -DCMAKE_BUILD_TYPE=Release
cmake --build build --config Release --parallel 2
ctest --test-dir build -C Release --output-on-failure
```

```powershell
# Windows / Visual Studio 2022
cmake -S example -B build -A x64 -DCMAKE_TOOLCHAIN_FILE="$PWD/wukong-sdk.cmake"
cmake --build build --config Release --parallel 2
ctest --test-dir build -C Release --output-on-failure
```

`wukong_example` 验证初始化和销毁；`wukong_chat` 可使用下节的 Alice/Bob 凭据交互收发。接入自己的应用时，保持上面的 `find_package` 和 `target_link_libraries`，将 CMake 的 toolchain 指向解压目录中的 `wukong-sdk.cmake`。

Unix 的 OpenSSL 为静态库；Windows 使用包内 DLL，发布应用时携带 CMake 复制到可执行文件旁的 DLL，并安装匹配的 Visual C++ Redistributable。Debug 运行库只用于开发。**预编译包使用 WSS 时，通过 `Options::caFile`（交互示例为 `WKIM_CA_FILE`）显式提供受维护的 CA 证书包**，不要依赖 OpenSSL 构建机的默认证书路径。

`BUILD_INFO.json` 记录 SDK 源码、registry 和打包提交；`FILES.sha256.json` 校验解压内容。升级时下载到新目录、校验哈希、使用新的 build 目录重新构建和验收，将应用与依赖一起更新，保留旧版本以便回滚。其他编译器、架构、CRT 或依赖组合使用 vcpkg/源码方式，不能任意混用二进制依赖。

三个平台的独立任务下载 ZIP 后，在新的目录编译 Debug/Release 消费端并运行生命周期与 26 项 WS/WSS 场景；Linux/macOS 另以固定 WuKongIM 服务端验收双向消息、重连和在线清理。Windows 的证据为协议夹具，未运行 Windows 服务端。完整兼容范围和发布验收见 [预编译包说明](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/v0.1.0/docs/PREBUILT.md)。

#### 正式包三节点验收

公开的 `v0.1.0` Linux x64 和 macOS arm64 压缩包已通过独立消费端的 Debug/Release 验收，服务端固定为 WuKongIM `5f5003778ccee6786591ed9968a5185e9213ea55`：三节点集群、256 个 Hash Slot、12 个逻辑 Slot、三个 Slot 副本，开启 Token 鉴权。C++ 和真实 JS `2.0.4` SDK 经带临时可信证书的 WSS 分别连接不同节点，验证双向消息及 SENDACK/接收消息标识对应、回包丢失后的请求超时、断网后不自动重发 SEND、接入节点退出与重启、存活节点持续通信，以及退出后的在线路由清理。查看[精确版本、验收记录和复现方法](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/abba280187987195614e2e25c89089475dca1979/docs/CLUSTER_VALIDATION.md)及通过的 [Linux/macOS 工作流](https://github.com/WuKongIM/WuKongEasySDK-CPP/actions/runs/34202657379)。

超时或连接错误**不代表消息没有送达**。故障测试中，接收方已收到消息，发送方却无法收到 SENDACK；应用应结合 `clientMsgNo` 和业务后端的历史记录核对不确定结果。重连会回到配置的 Gateway URL，不会自动发现替代地址或同步离线历史。

本次是同一台主机上的三个进程和 TLS 终止代理的有界测试，不覆盖跨主机网络分区、生产 CA 配置、Windows 服务端行为、容量或长时间稳定性。

#### 上线前检查

早期源码验收另外覆盖协议与 WS/WSS 测试、内存与未定义行为检测、安装后的下游编译，以及真实单节点集群上的双向消息、重连、错误 Token 拒绝和在线清理。上面的正式包三节点验收单独记录产物和服务端版本，不能把源码测试归于其他二进制版本。

继续验收实际目标系统、真实网络 WSS、代理路径、Token 轮换、重复投递、容量、监控与回滚。SDK 不提供离线恢复、会话、未读或推送；这些需求请使用完整版 SDK。回到 [WuKongEasySDK 概览](https://docs.githubim.com/zh/sdk/easy)，或继续[上线检查](https://docs.githubim.com/zh/guide/integration/acceptance)。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/cpp/getting-started.en.mdx)

[WuKongEasySDK-CPP](https://github.com/WuKongIM/WuKongEasySDK-CPP) follows JS `v2.0.4` for WebSocket JSON-RPC CONNECT, online messaging, automatic RECVACK, heartbeat, reconnect, and custom events. It uses C++17, Boost.Beast, OpenSSL, and nlohmann/json.

  This tutorial pins project `0.1.0` source `3e367a908f42385ab9306f9708b7456399cace7d`, available through the WuKongIM-maintained vcpkg Git registry or CMake source installation. Version v0.1.0 also provides prebuilt SDK archives; this registry is not Microsoft’s curated catalog. This source connected to WuKongIM `132e46209d98fa0425cc0f88e7a97080cdad044d` with Token authentication enabled in a 256-hash-slot single-node cluster and completed C++/C++ and C++/JS bidirectional messaging. See the repository [validation record](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/3e367a908f42385ab9306f9708b7456399cace7d/docs/VALIDATION.md) for the exact scope.

#### 2. Recommended: vcpkg + CMake

Install [vcpkg](https://learn.microsoft.com/en-us/vcpkg/get_started/get-started)
and set `VCPKG_ROOT` to its directory. Keep Git, CMake 3.20+ and a C++17 compiler
available (Visual Studio 2022 on Windows, Xcode command-line tools on macOS,
GCC/Clang on Linux). The tested vcpkg revision is
`04a9d8e5212d01ee1dd9478eadd9caade4f8b0d4`.

In your application directory, create `vcpkg.json`:

```json
{"dependencies": ["wukong-easy-sdk"]}
```

`vcpkg-configuration.json`:

```json
{
  "default-registry": {
    "kind": "git",
    "repository": "https://github.com/microsoft/vcpkg",
    "baseline": "04a9d8e5212d01ee1dd9478eadd9caade4f8b0d4"
  },
  "registries": [
    {
      "kind": "git",
      "repository": "https://github.com/WuKongIM/WuKongEasySDK-CPP.git",
      "baseline": "63ec99d34c7605b64e2173d201639042e0e49de9",
      "packages": [
        "wukong-easy-sdk"
      ]
    }
  ]
}
```

Add the following `CMakeLists.txt` next to your own `main.cpp`:

```cmake
cmake_minimum_required(VERSION 3.20)
project(my_app LANGUAGES CXX)
find_package(WuKongEasySDK 0.1 CONFIG REQUIRED)
add_executable(my_app main.cpp)
target_link_libraries(my_app PRIVATE WuKongEasySDK::WuKongEasySDK)
```
```sh
# Linux / macOS
cmake -S . -B build -DCMAKE_TOOLCHAIN_FILE="$VCPKG_ROOT/scripts/buildsystems/vcpkg.cmake" -DCMAKE_BUILD_TYPE=Release
cmake --build build --config Release --parallel 2
```

```powershell
# Windows / Visual Studio 2022
cmake -S . -B build -DCMAKE_TOOLCHAIN_FILE="$env:VCPKG_ROOT/scripts/buildsystems/vcpkg.cmake" -DVCPKG_TARGET_TRIPLET=x64-windows
cmake --build build --config Release --parallel 2
```

vcpkg installs the SDK, Boost, OpenSSL and JSON automatically. The first build
may compile dependencies and take several minutes; this is not a prebuilt
archive. The SDK port is a static library; on Windows, distribute dependency
DLLs copied beside the application when using `x64-windows`.

This public Git registry is maintained by WuKongIM in this repository. It is
not Microsoft's curated catalog: copy the registry configuration as well as
the dependency manifest. SDK source is pinned to
`3e367a908f42385ab9306f9708b7456399cace7d`, independently of the registry baseline.
Commit both JSON files to reproduce dependency selection. Existing projects
should merge these entries into their manifests instead of overwriting them.

As of 2026-09-08, the curated-catalog submission is [microsoft/vcpkg#53837](https://github.com/microsoft/vcpkg/pull/53837) and has not been merged. The proposed port builds the official `v0.1.0` tag; the instructions above still use the WuKongIM custom registry and require `vcpkg-configuration.json`.

[Independent consumer example](https://github.com/WuKongIM/WuKongEasySDK-CPP/tree/main/examples/vcpkg-consumer) · [Registry maintenance](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/main/docs/VCPKG.md)

#### Alternative: download and extract a prebuilt SDK

To skip dependency compilation, download the matching ZIP and `SHA256SUMS` from [C++ SDK v0.1.0 Release](https://github.com/WuKongIM/WuKongEasySDK-CPP/releases/tag/v0.1.0), verify its SHA-256 and extract it. You only need CMake 3.20+ and a compatible C++ development environment; no separate vcpkg installation is required.

| Archive suffix | Consumer environment |
| --- | --- |
| `linux-x64-gcc13.zip` | Ubuntu 24.04 x64, GCC 13, libstdc++ C++11 ABI, glibc 2.39+ |
| `macos-arm64-appleclang.zip` | macOS 14+ arm64, Apple Clang, libc++ |
| `windows-x64-msvc143-md.zip` | Windows x64, Visual Studio 2022 v143; Release `/MD`, Debug `/MDd` |

Names start with `WuKongEasySDK-CPP-0.1.0-`. Each archive includes Debug/Release static SDK libraries, Boost/JSON headers, OpenSSL libraries, licenses and a minimal example. From the extracted directory:

```sh
# Linux / macOS
cmake -S example -B build -DCMAKE_TOOLCHAIN_FILE="$PWD/wukong-sdk.cmake" -DCMAKE_BUILD_TYPE=Release
cmake --build build --config Release --parallel 2
ctest --test-dir build -C Release --output-on-failure
```

```powershell
# Windows / Visual Studio 2022
cmake -S example -B build -A x64 -DCMAKE_TOOLCHAIN_FILE="$PWD/wukong-sdk.cmake"
cmake --build build --config Release --parallel 2
ctest --test-dir build -C Release --output-on-failure
```

`wukong_example` verifies initialization and destruction; `wukong_chat` supports interactive messaging with the Alice/Bob credentials below. For your application, retain the `find_package` and `target_link_libraries` above and point CMake's toolchain option to the extracted `wukong-sdk.cmake`.

OpenSSL is static on Unix and uses bundled DLLs on Windows. Deploy Windows applications with the DLLs CMake copies beside the executable and a compatible Visual C++ Redistributable. Debug runtimes are for development only. **For WSS with prebuilt packages, supply a maintained CA bundle explicitly via `Options::caFile` (`WKIM_CA_FILE` in the chat example)**; do not depend on OpenSSL's build-machine default certificate path.

`BUILD_INFO.json` identifies the SDK source, registry and packaging commits; `FILES.sha256.json` verifies extracted content. Upgrade into a separate directory, check hashes, rebuild in a new build directory and rerun acceptance. Update the application and dependencies together and retain the previous version for rollback. Use vcpkg/source for other compilers, architectures, CRTs or dependency combinations; binary dependencies cannot be mixed arbitrarily.

Fresh jobs download each platform's ZIP and compile Debug/Release consumers at a different path, running lifecycle checks and 26 WS/WSS scenarios per configuration. Linux/macOS also use the pinned WuKongIM server to verify bidirectional messaging, reconnect and presence cleanup. Windows evidence uses the protocol fixture, not a Windows server. See [prebuilt package documentation](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/v0.1.0/docs/PREBUILT.md) for compatibility and release acceptance.

#### Released-package three-node verification

The public `v0.1.0` Linux x64 and macOS arm64 archives have also passed an independent Debug/Release consumer test against WuKongIM `5f5003778ccee6786591ed9968a5185e9213ea55`: three nodes, 256 hash slots, 12 logical Slots, three Slot replicas and Token authentication enabled. C++ and the actual JS `2.0.4` SDK connect to different nodes through WSS with temporary trusted certificates. The harness checks bidirectional messaging and matching SENDACK/receive IDs, withheld-ACK timeouts, connection loss without automatic SEND replay, ingress-node crash/restart, continued traffic between surviving nodes, and online-route cleanup. See the [exact inputs, receipt and repeatable test](https://github.com/WuKongIM/WuKongEasySDK-CPP/blob/abba280187987195614e2e25c89089475dca1979/docs/CLUSTER_VALIDATION.md) and the successful [Linux/macOS workflow](https://github.com/WuKongIM/WuKongEasySDK-CPP/actions/runs/34202657379).

A timeout or connection error does **not** prove that a message was not delivered. In the fault test, the receiver already has the message while the sender cannot receive its SENDACK. Reconcile uncertain outcomes using `clientMsgNo` and your backend's history policy. Reconnection returns to the configured Gateway URL; it does not automatically discover a replacement URL or synchronize offline history.

This bounded test uses three processes on one host and a TLS termination proxy. It does not establish multi-host network-partition behavior, production CA configuration, Windows product-server behavior, capacity or long-duration stability.

#### Before production

Earlier source validation separately covers protocol and WS/WSS tests, memory and undefined-behavior instrumentation, installed consumers, and real single-node-cluster messaging, reconnect, invalid-token rejection and presence cleanup. The released-package three-node evidence above names its own artifact and server revisions; source-only results must not be attributed to a different binary.

Also validate the actual target system, real-network WSS, proxy path, Token rotation, duplicate delivery, capacity, monitoring, and rollback. This SDK does not provide offline recovery, conversations, unread counts, or push; choose a full SDK for those requirements. Return to the [WuKongEasySDK overview](https://docs.githubim.com/en/sdk/easy), or continue with [Release Checks](https://docs.githubim.com/en/guide/integration/acceptance).


## rust


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/rust/getting-started.mdx)

WuKongEasySDK-Rust 适合原生 Rust 程序、桌面应用和已有业务后端的通信客户端。它参考 [WuKongEasySDK-JS 2.0.4](https://github.com/WuKongIM/WuKongEasySDK-JS/tree/9c03c98c725982fac224cd1d3b52456eae983975)，使用 WebSocket JSON-RPC CONNECT 完成鉴权，再收发在线消息。

当前 Product Gateway 支持这条在线双向收发路径。SDK 不提供本地消息库、会话/未读、离线恢复或推送；需要这些能力时先阅读 [SDK 选择](https://docs.githubim.com/zh/sdk)。

#### 弱网、队列与退出

`Backpressure` 表示请求未获准进入发送队列：应用应限制生产并发，避免不断堆积重试任务。
发送超时可能发生在另一客户端已经收到消息之后，应保留“结果未知”，由应用依据幂等约定决定是否重试。
`RecvError::Lagged` 表示监听器错过了事件，需要应用侧补偿；自动 RECVACK 不提供持久化收件箱。

仓库验收工具反复执行两个客户端的 WSS 生命周期，注入每个数据块 20/40/60 ms 延迟、
返回方向暂停、双向黑洞和连接中断。通过两个待完成 SEND 检查准入上限，通过保留
16 个事件的慢监听器检查显式丢失通知。验收使用 800 ms SEND 超时和 3 秒 pong 超时，
这些是测试参数，与 SDK 默认值不同。

代理侧有界 WebSocket 审计要求每轮恰好 58 个出站 SEND，与 58 次已核对投递匹配，
避免服务端去重掩盖额外重发；审计仅保留计数，不支持的帧格式直接使验收失败。

每轮销毁两个客户端，并等待两个代理的连接流和任务都归零，再采样 Rust 探针的 RSS
和文件描述符。前三轮预热后，固定允许相对基线增加 64 MiB RSS 和八个文件描述符。
这些有限观测不证明生产容量、多日稳定性或不存在任何资源泄漏。

验证独立下载的正式包时，先按仓库 README 准备固定服务端提交，再运行：

```sh
python3 tests/acceptance/run.py --server-source test-server --distribution registry --seconds 120 --network-seconds 1800 --output .acceptance/network-1800s.json
```

弱网循环本身至少运行 30 分钟，构建和已有单聊/群聊检查另计。日常 CI 默认循环
30 秒且至少四轮，长程模式由手动运行明确选择。回执的 `network` 字段记录故障结果、
恢复耗时、资源采样和清理结果。资源观测支持 Linux 和 macOS。

#### WSS 私有 CA

使用私有 CA 时，将 DER 根证书字节加入 `Options.additional_root_certificates`。
最多 16 张，每张不超过 64 KiB，不接收私钥；公共 WebPKI 根仍保留，域名与有效期校验始终启用。

```rust
let options = wukong_easy_sdk::Options {
    additional_root_certificates: vec![std::fs::read("company-root.der")?],
    ..Default::default()
};
```

持续收发需要包含 [WebSocket 缓冲区修复](https://github.com/WuKongIM/WuKongIM/pull/901) 的服务端版本。
单节点集群验收固定 `27a39f15bf163b433f417b78ab6bfc6e589585e5`；旧版在连续收发时可能损坏入队报文并断开连接。

#### 协议与上线前检查

- 发送使用字符串请求 ID、camelCase 字段与 Base64 UTF-8 JSON；接收支持 JSON 对象及 Base64 JSON；结果兼容 camelCase、snake_case 及同时出现的两种拼写。
- SDK 默认静默；Auth 的 Debug 和 SDK 错误不包含 Token、URL、原始帧或服务端错误正文。完整事件包含业务数据，不应直接打印。
- 自定义事件暴露 `id`、`event_type`、`timestamp` 和 `data`；支持解析不意味着服务端部署一定产生某类事件。
- 上线前检查 WSS 证书、代理 Upgrade、Token 撤销/轮换、目标操作系统、丢包、队列上限和退出；单次在线验收不代替长期稳定性和容量测试。

#### 三节点集群验收

独立的[集群验收脚本](https://github.com/WuKongIM/WuKongEasySDK-Rust/blob/main/tests/acceptance/cluster.py)
固定包含[跨节点成员缓存修复](https://github.com/WuKongIM/WuKongIM/pull/920)的服务端
`f041174a042b4a96179218571e06c04bb64cf1ca`，启动三个隔离进程，
配置 256 个 Hash Slot、12 个逻辑 Slot 和三个 Slot 副本。
四个 Rust 客户端通过私有 CA 验证的 WSS，分别接入节点 `1、2、3、2`。
验收覆盖前三个客户端之间全部六条有向单聊路径、群成员投递、频道隔离、
移除成员与黑名单拒绝，以及节点重启后的权限保持。

脚本先阻断发送者的返回流量，验证 SEND 超时而接收者已经收到消息；
随后强制终止接入节点 1，使用原地址、原数据目录重启。
原有客户端必须自动重连到节点 1。Slot 权威变更会清空易失的在线路由，
因此脚本还要求从三个 API 入口连续两个 25 秒心跳周期都能查询到四个用户在线，
再检查跨节点投递：观察窗口为 50 秒，整个路由检查最多等待 100 秒。
回执分别记录连接恢复时间和路由稳定检查完成时间。
独立的 WebSocket 出站计数必须与每次应用 SEND 匹配，包含被拒绝和结果未知的发送，
避免服务端去重掩盖客户端自动重发。应用不会重试结果未知的消息。试运行曾在 CONNECT 恢复后立即发送，
观察到接收者已不在在线路由列表、消息有 SENDACK 却没有投递。
因此 CONNECT 和 SENDACK 都不能作为端到端恢复或接收者已收到消息的证明。

这一限时场景验证原地址恢复后的重连，不提供其他地址自动选择，
也不代表节点停机期间连续投递、指定 Channel Leader 切换、网络分区、离线补拉或大群容量已经验证。
每个投递阶段至少观察被排除的客户端 500 ms。
测试配置为 SEND 超时 3 秒、连接超时 2 秒、PONG 超时 10 秒、最多重连 100 次、
重试退避 100–500 ms，与 SDK 默认值不同。

```bash
# 在 WuKongEasySDK-Rust 中运行；服务端使用下文记录的精确、干净源码。
RUSTUP_TOOLCHAIN=1.86.0 python3 tests/acceptance/cluster.py \
  --server-source ../test-server --distribution registry --seconds 600
```

CI 分别对源码和正式包运行 60 秒工作负载，手动运行可选择 600 秒。
每次运行还包括开始时的权限与故障检查，并在计时工作负载中再次终止、重启接入节点。
只有所有客户端完成销毁、查询四个用户得到空的在线状态列表，且所有自有服务端进程、
代理连接和任务完成清理，才算通过。回执分别记录正式包、验收脚本和服务端身份。

[600 秒正式包三节点回执](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/download/v0.1.0/registry-cluster-0.1.0-macos-600s.json)
分别记录干净验收工具提交 `6b533a25ff0c61548a3f90dd36fa2562118f8f21`、
精确的 0.1.0 正式包和上述已合并的服务端提交。
`workload_seconds` 包含中途故障与路由观察；`fault_to_reconnected_ms`
记录连接恢复时间，`fault_to_stable_routes_ms` 还包含 50 秒的路由观察窗口。
`application_sends` 必须等于 `wire_sends`；`deliveries` 按所有预期接收者计数，
群聊多接收者投递会使其与发送次数不同。通过必须完成至少连续 600 秒工作负载、
两次故障阶段和全部清理，中断的候选版本运行不会拼接计入。现有 0.1.0 包不变。

#### 验证记录

2026-09-08，crates.io `wukong-easy-sdk 0.1.0` 发布自源码 `5b4a59cdbb66a9e0c3878e73ba4656f08ee05c6b`。独立空缓存消费者从公共 registry 下载正式包，编译通过本页中英文的连接收发和私有 CA 示例。下载包 SHA-256 为 `0029747f10b86f566e2d659535df0954114769a90962e562fb522a95e5508719`，与 [GitHub Release](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/tag/v0.1.0) 附件一致。

随后完成了 **crates.io 正式包端到端验收**：macOS Rust 1.86 的独立空缓存消费者下载精确发布包，通过 Rust/Rust 收发、错误 Token 拒绝，以及 120 秒 Rust/JS WSS 验收，确认 1,747 次回显和三次断网恢复，无重复或事件丢失，所有进程完成清理。一次中断发送保留结果未知语义。[正式包回执](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/download/v0.1.0/registry-e2e-0.1.0-macos-120s.json) 分别记录验收工具源码 `cfa48a038c2cfd56948ace43afe3b2f5f91dace3` 与发布包源码；服务端仍为 `27a39f15bf163b433f417b78ab6bfc6e589585e5`。

新增的 **正式包群聊验收** 同样通过：四个 Rust 客户端、两个群、十个场景，
确认 15 次预期投递。覆盖成员投递与群间隔离、非成员拒绝、成员添加/移除/重新加入、
黑名单拒绝与解除，以及四个客户端断网自动重连后的成员权限。每条消息都匹配
SENDACK 标识、发送人、群和正文，未观察到重复或事件丢失；每个场景至少观察
500 ms，检查被排除身份未收到消息。[群聊回执](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/download/v0.1.0/registry-group-0.1.0-macos-120s.json)
绑定干净验收工具提交 `0262de9454603ee528dd2d9d9f236dec89e8df2a`，使用相同的正式包
与服务端版本、macOS Rust 1.86；配套 120 秒 Rust/JS 验证确认 2,608 次回显、三次恢复，
一次中断发送保留结果未知语义。所有客户端和测试进程均完成清理。这是四客户端的
单节点集群验证，不证明大群容量、离线恢复或跨节点路由；本次无需修改 SDK 运行时代码或发布新包。

[30 分钟弱网与资源回执](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/download/v0.1.0/registry-network-0.1.0-macos-1800s.json)
单独保留验收工具提交 `2bd61a986f4f69418b3ec95de6c272408fc6f3b5`，正式包仍为上述
0.1.0，服务端仍使用上述固定提交。`network.cycles` 记录出站 SEND 数、预期投递、
显式超时/背压/监听器落后及恢复结果，`network.samples` 记录连接清空后的 RSS 和文件描述符。
成功回执要求完整 1,800 秒循环、最后一轮完成和进程清理，不将多次短程运行拼接为长程结果。
资源阈值、测试参数和有限观测范围见上文。

同一源码通过 [跨平台 CI](https://github.com/WuKongIM/WuKongEasySDK-Rust/actions/runs/34190886754)：27 项协议、生命周期和 TLS 测试，以及 Clippy、API 文档和打包校验。覆盖 Linux Rust 1.86/stable、macOS 和 Windows stable。

[真实服务端 CI](https://github.com/WuKongIM/WuKongEasySDK-Rust/actions/runs/34190886664) 使用 WuKongIM `27a39f15bf163b433f417b78ab6bfc6e589585e5` 的 256 Hash Slot 单节点集群，开启 Token 校验并拒绝错误 Token。Rust/Rust 验证通过后，与 npm `easyjssdk@2.0.4` 完成 120 秒 WSS 收发、3,012 次 Unicode 回显、三次断网恢复及清理，未观察到重复或事件丢失。五项 TLS 测试另行验证可信 CA、不可信 CA、错误域名、过期证书和无效配置。

另有源码 `f30f1b32d0628f1e909fc21da704e5e49bc9f63e` 的 600 秒 macOS 验收记录：13,758 次回显及三次恢复。该历史记录与正式包安装验证分别保留。旧服务端 `132e46209d98fa0425cc0f88e7a97080cdad044d` 仅通过早期短程验证，未通过持续收发，不应继承修复后服务端的验收结论。

公共 registry 正式包端到端验收与历史源码验证各自证明对应范围，不代表物理设备、离线恢复、容量或多日稳定性。返回 [WuKongEasySDK](https://docs.githubim.com/zh/sdk/easy) 查看其他平台。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/rust/getting-started.en.mdx)

WuKongEasySDK-Rust serves native Rust programs, desktop applications and communication clients with an existing application backend. It follows [WuKongEasySDK-JS 2.0.4](https://github.com/WuKongIM/WuKongEasySDK-JS/tree/9c03c98c725982fac224cd1d3b52456eae983975), authenticating through WebSocket JSON-RPC CONNECT before exchanging online messages.

The current Product Gateway supports this online bidirectional messaging path. The SDK does not provide a local message store, conversations/unread state, offline recovery or push. Read [SDK selection](https://docs.githubim.com/en/sdk) when you need those capabilities.

#### Weak networks, queues and shutdown

Handle `Backpressure` as admission rejection: bound producer concurrency instead
of accumulating unbounded retry tasks. A SEND timeout can occur after another
client has already received the message; preserve the unknown outcome and let
the application decide whether to retry under its idempotency contract.
`RecvError::Lagged` means the observer missed events and needs application-level
reconciliation. Automatic RECVACK does not provide a durable application inbox.

The repository acceptance harness repeats two-client WSS lifecycles with
20/40/60 ms per-chunk delay, blocked return traffic, bidirectional blackholes and
transport aborts. It tests admission at two pending SENDs and a deliberately
slow observer with 16 retained events. These are test settings: SEND timeout
800 ms and pong timeout 3 s, distinct from SDK defaults.

A bounded proxy-side WebSocket audit requires exactly 58 outbound SENDs per
cycle, matching 58 verified deliveries. Server deduplication cannot hide an extra
retransmission from this count; the audit retains counts only and fails on
unsupported framing.

After each cycle both clients are destroyed, and both proxies must have zero
streams and tasks before sampling the Rust probe's RSS and file descriptors.
After three warmup cycles, the fixed growth allowance is 64 MiB RSS and eight
file descriptors. These bounded observations do not establish production
capacity, multi-day stability or the absence of every resource leak.

For an independently downloaded public package, use the pinned server checkout
from the repository README and run:

```sh
python3 tests/acceptance/run.py --server-source test-server --distribution registry --seconds 120 --network-seconds 1800 --output .acceptance/network-1800s.json
```

The weak-network loop lasts at least 30 minutes, in addition to the existing
person/group checks and build time. CI defaults to a 30-second loop with at
least four cycles; the longer mode is an explicit manual run. The nested
`network` receipt retains fault outcomes, recovery timings, resource samples
and cleanup results. Supported measurement hosts are Linux and macOS.

#### Private CA for WSS

For private PKI, put DER root bytes in `Options.additional_root_certificates`.
This accepts at most 16 certificates of 64 KiB each, never private keys. Public
WebPKI roots remain trusted; hostname and expiry verification stay enabled.

```rust
let options = wukong_easy_sdk::Options {
    additional_root_certificates: vec![std::fs::read("company-root.der")?],
    ..Default::default()
};
```

Sustained messaging requires a server containing the [WebSocket buffer fix](https://github.com/WuKongIM/WuKongIM/pull/901).
Single-node cluster acceptance pins `27a39f15bf163b433f417b78ab6bfc6e589585e5`; the older server can corrupt queued frames and disconnect during repeated exchanges.

#### Protocol and before production

- Sends use string request IDs, camelCase metadata and Base64 UTF-8 JSON. Receive supports objects and Base64 JSON; results accept camelCase, snake_case and both spellings together.
- The SDK is default-silent. Auth Debug and SDK errors omit tokens, URLs, raw frames and raw server error text. Complete events contain application data and must not be logged wholesale.
- Custom events expose `id`, `event_type`, `timestamp` and `data`. Parser support does not imply a deployment produces particular event types.
- Before production, validate WSS certificates, proxy Upgrade, token revocation/rotation, target OS, packet loss, queue bounds and shutdown. One online run does not establish capacity or long-term stability.

#### Three-node cluster acceptance

The dedicated [cluster harness](https://github.com/WuKongIM/WuKongEasySDK-Rust/blob/main/tests/acceptance/cluster.py)
pins server `f041174a042b4a96179218571e06c04bb64cf1ca`, containing the
[cross-node membership cache fix](https://github.com/WuKongIM/WuKongIM/pull/920),
and runs three isolated server processes with 256 hash slots, 12 logical slots and
three Slot replicas. Four Rust clients authenticate over verified private-CA WSS
on ingress nodes `1, 2, 3, 2`. It checks all six directed person-message paths
between the first three clients, group fanout, channel isolation, removed-member
and denylist rejection, and those permissions after a node restart.

The harness withholds the sender's return traffic: a SEND times out while its
peer receives the message. It then kills ingress node 1 and restarts the same
address and durable directory. The existing client must automatically reconnect
to node 1. Because Slot authority changes clear volatile presence, the harness
then requires all four users to remain online through every API ingress for two
25-second heartbeat intervals (a 50-second observation window, bounded by a
100-second gate) before checking resumed cross-node delivery. Connection recovery
and completion of this route stability gate are recorded separately. An independent WebSocket wire counter
must match every application SEND, including rejected and uncertain sends, so
server deduplication cannot conceal an automatic replay. The application never
retries the uncertain send. Exploratory runs sent immediately after CONNECT
recovery and observed an accepted SEND without delivery while the recipient was
absent from the online route view. CONNECT and SENDACK therefore must not be
interpreted as proof of end-to-end recovery or recipient delivery.

This proves recovery to the original endpoint in this bounded scenario. It does
not implement alternate-address selection, demonstrate uninterrupted delivery
while a node is down, or verify a selected Channel leader transfer, network
partition, offline catch-up or large-group capacity. Each delivery phase observes
excluded clients for at least 500 ms. Test settings use a 3-second SEND deadline,
2-second connection and 10-second PONG deadlines, 100 reconnect attempts, and 100–500 ms retry
backoff; these differ from SDK defaults.

```bash
# Run from WuKongEasySDK-Rust; use the exact clean server checkout cited below.
RUSTUP_TOOLCHAIN=1.86.0 python3 tests/acceptance/cluster.py \
  --server-source ../test-server --distribution registry --seconds 600
```

CI runs a separate 60-second workload for source and registry distributions;
manual runs can select 600 seconds. Each run also executes the initial permission
and crash suite, with a second ingress crash during the timed workload. Successful
completion requires all clients destroyed, an empty online-status response for all four users, and
all owned server processes, proxy streams and tasks stopped. Package, harness and
server identities remain separate in the receipt.

The [600-second registry cluster receipt](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/download/v0.1.0/registry-cluster-0.1.0-macos-600s.json)
binds clean harness `6b533a25ff0c61548a3f90dd36fa2562118f8f21` to the exact
0.1.0 public package and the merged server revision above. `workload_seconds`
includes the midpoint fault and route observation; `fault_to_reconnected_ms`
measures socket recovery, while `fault_to_stable_routes_ms` also includes the
50-second route observation. `application_sends` must equal `wire_sends`;
`deliveries` counts all expected recipients, so group fanout makes it a different
total. A passing result requires at least 600 uninterrupted workload seconds,
both crash phases and complete cleanup. Interrupted candidate runs are not added
to this duration. The existing 0.1.0 crate is unchanged.

#### Verification record

On 2026-09-08, crates.io `wukong-easy-sdk 0.1.0` was published from source `5b4a59cdbb66a9e0c3878e73ba4656f08ee05c6b`. An independent consumer with an empty Cargo cache downloaded the public registry package and compiled this tutorial's Chinese and English connection/messaging and private CA examples. The downloaded archive SHA-256 is `0029747f10b86f566e2d659535df0954114769a90962e562fb522a95e5508719`, matching the [GitHub Release](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/tag/v0.1.0) attachment.

A subsequent **public-package end-to-end run** used an empty Cargo cache and the exact crates.io archive on macOS Rust 1.86: Rust/Rust roundtrip, invalid-Token rejection, 1,747 confirmed Rust/JS WSS echoes in 120 seconds, three forced cuts/recoveries, no duplicates or event loss, and complete cleanup. One interrupted send retained unknown-outcome semantics. The [registry receipt](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/download/v0.1.0/registry-e2e-0.1.0-macos-120s.json) records harness `cfa48a038c2cfd56948ace43afe3b2f5f91dace3` separately from the released package source; the server remains `27a39f15bf163b433f417b78ab6bfc6e589585e5`.

The **group acceptance** extension also passed with the exact registry package:
four Rust clients, two groups, ten phases and 15 expected deliveries. It verified
fanout and channel isolation, nonmember rejection, member add/remove/re-add,
denylist rejection/removal, and membership after all four clients automatically
reconnected. Each delivery matched the SENDACK identity, sender, channel and
payload, without duplicates or observer lag. Excluded clients were observed for
at least 500 ms per phase. The [group receipt](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/download/v0.1.0/registry-group-0.1.0-macos-120s.json)
binds clean harness `0262de9454603ee528dd2d9d9f236dec89e8df2a` to the same package
and server revisions on macOS Rust 1.86; its accompanying 120-second Rust/JS run
confirmed 2,608 echoes and three recoveries, with one unknown-outcome interrupted
send. All clients and owned processes were cleaned up. This is a four-client
single-node cluster check, not large-group capacity, offline recovery or
cross-node routing evidence. No SDK runtime change or new crate release was
needed for this validation.

The [30-minute weak-network and resource receipt](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/download/v0.1.0/registry-network-0.1.0-macos-1800s.json)
keeps harness `2bd61a986f4f69418b3ec95de6c272408fc6f3b5` separate from the unchanged
0.1.0 package and pinned server above. Inspect `network.cycles` for outbound SEND
counts, expected deliveries, explicit timeouts/backpressure/lag and recovery
results, and `network.samples` for quiescent RSS/file-descriptor observations.
A successful receipt requires the entire 1,800-second loop, its last complete
cycle and process cleanup; separate short runs are never combined into that
result. The finite resource bounds and test settings are described above.

The same source passed [cross-platform CI](https://github.com/WuKongIM/WuKongEasySDK-Rust/actions/runs/34190886754): 27 protocol, lifecycle and TLS tests, Clippy, API documentation and package verification on Linux Rust 1.86/stable, macOS stable and Windows stable.

[Real-server CI](https://github.com/WuKongIM/WuKongEasySDK-Rust/actions/runs/34190886664) used WuKongIM `27a39f15bf163b433f417b78ab6bfc6e589585e5`, a 256 Hash Slot single-node cluster with Token validation and explicit incorrect-Token rejection. After Rust/Rust checks, Rust exchanged 3,012 Unicode echoes with npm `easyjssdk@2.0.4` over WSS for 120 seconds, recovered from three transport interruptions and cleaned up. No duplicate echoes or event loss were observed. Five separate TLS tests cover trusted CA, unknown CA, wrong hostname, expiry and invalid configuration.

A separate 600-second macOS receipt for source `f30f1b32d0628f1e909fc21da704e5e49bc9f63e` records 13,758 echoes and three recoveries. That historical receipt remains separate from registry installation verification. Earlier server `132e46209d98fa0425cc0f88e7a97080cdad044d` passed only the initial short smoke and failed sustained messaging; it must not inherit the fixed server's acceptance results.

Public registry end-to-end acceptance and historical source runs establish their respective scopes; neither proves physical-device behavior, offline recovery, capacity or multi-day stability. Return to [WuKongEasySDK](https://docs.githubim.com/en/sdk/easy) for other platforms.


## python


### zh source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/python/getting-started.mdx)

[WuKongEasySDK-Python](https://github.com/WuKongIM/WuKongEasySDK-Python) 参考 JS `v2.0.4`，使用 Python 3.11+、`asyncio` 与 `websockets`，提供 WebSocket JSON-RPC CONNECT、在线收发、自动 RECVACK、心跳、重连和自定义事件。

#### 6. WSS 与验证范围

WSS 默认验证证书链与主机名，最低 TLS 1.2。私有 CA 使用 `WKIMOptions(ca_file="/path/ca.pem")`，交互示例读取 `WKIM_CA_FILE`。不提供跳过校验的选项；不自动读取系统代理，直接使用传入的 Gateway/代理 URL。

默认不输出日志；`WKIMOptions(debug_logging=True)` 只启用固定生命周期元数据，不记录 Token、Payload、URL、原始帧、服务端响应文本或底层异常对象。

PyPI `0.1.0` 来自发布源码 `ec2c62c73eca29be99ac15ba76ff7466c13617d5`。公开 wheel 与 sdist 的 SHA-256 均与发布工作流产物一致。从 PyPI 新安装的 Python `3.11.12` / websockets `15.0.1` 和 Python `3.14.7` / websockets `17.1` 两种环境，各通过 64 项测试，并在 WuKongIM `0348c0539bbee420a859439695acdac911afa854`、开启 Token 鉴权的 256 Hash Slot 单节点集群上完成 Python/Python 与真实 JS `2.0.4` 双向消息、Ping、手动重新连接、错误 Token 拒绝和在线清理。JS 来自固定源码构建。独立 WS/WSS 测试覆盖自动重连、请求取消、队列边界和证书拒绝。完整哈希、安装回执、版本与复现命令见 [PyPI 正式包验证记录](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/90018a9ac53bdef4cb15dadb169ff835cbfcb2a6/docs/PYPI_VALIDATION.md)。

另外提供独立的[三节点 WSS 验收流程](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/a9be49e69f4b7155a9a6244ef63f9cb6baf00cb9/docs/CLUSTER_ACCEPTANCE.md)：从 PyPI 安装固定版本，通过私有 CA TLS 代理连接三个真实节点，覆盖跨节点收发、丢失 SENDACK、断网重连、节点重启、Token 轮换与资源清理。日常 CI 跑短验收，手动运行可选择 30 或 60 分钟；每次结果单独记录包、服务端、JS 与测试脚本身份。

同一 PyPI `0.1.0` 随后在 WuKongIM `e7ef61ba702e045648b9fa535f051e5b2ee4a1db`、256 Hash Slot、12 个物理 Slot、每个 Slot 三副本的本机三节点集群上完成 **30 分钟 WSS 验收**，核对 **35,380 条 SENDACK/RECV**。6 次断网与 ACK 丢失、入口节点重启、三节点 Token 轮换均通过；未观察到重复回调，连接数和异步任务数保持稳定，退出后进程、连接与额外任务均清零。该验收使用明确配置的故障测试超时和重试参数；完整版本、资源采样和原始 JSON 见[三节点 WSS 验证记录](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/a9be49e69f4b7155a9a6244ef63f9cb6baf00cb9/docs/CLUSTER_VALIDATION.md)，不代表生产容量或恰好一次投递保证。

不提供离线同步、会话、未读或推送；这些需求请使用完整版 SDK。继续验证实际部署的 WSS 代理、Token 轮换、重复投递和容量，或回到 [WuKongEasySDK 概览](https://docs.githubim.com/zh/sdk/easy)与[运行官方示例](https://docs.githubim.com/zh/sdk/easy/examples)。

#### 7. 在线群聊与成员权限

PyPI 包继续使用 `wukong-easy-sdk==0.1.0`。群聊通过同一个连接、`WKIMChannelType.GROUP`（`2`）和 `WKIMEvent.MESSAGE` 收发，不需要客户端订阅接口：

```python
await im.send("team-chat", WKIMChannelType.GROUP, {"type": 1, "content": "群内消息"})
```

**先由可信后端创建群、添加成员并提供设备类别 `2` 的 Token。** 以下 Product HTTP 请求只在可信服务端执行；不要把管理地址或权限交给不可信客户端。示例将 Alice 和 Bob 加入群，并明确禁止非成员发送：

```http
POST /channel
Content-Type: application/json

{"channel_id":"team-chat","channel_type":2,"allow_stranger":0,"reset":1,"subscribers":["alice","bob"]}
```

`reset:1` 会替换已有群的成员，仅对新建示例群使用。日常通过 `/channel/subscriber_add`、`/channel/subscriber_remove` 修改成员；请求字段为 `channel_id`、`channel_type:2`、`subscribers`。黑名单使用 `/channel/blacklist_add`、`/channel/blacklist_remove`，成员字段为 `uids`。这些操作均由后端控制。

下载新增群聊示例的固定源码；原 `v0.1.0` 标签不包含这个新增文件。示例源码和已安装的 PyPI 包版本分别固定：

```sh
git clone https://github.com/WuKongIM/WuKongEasySDK-Python.git WuKongEasySDK-Python-group
git -C WuKongEasySDK-Python-group checkout --detach 527f37c876326e7ad3cc48c89828c4c3ffed09fc
export WKIM_UID=alice
export WKIM_GROUP=team-chat
# 通过安全环境设置 WKIM_TOKEN、WKIM_URL；私有 CA 可设置 WKIM_CA_FILE。
python WuKongEasySDK-Python-group/examples/group_chat.py
```

另一终端使用 Bob 的 UID 和 Token、相同群 ID。双方在线后输入消息，`/quit` 退出。示例显示消息内容及数字错误码。上述策略下，非成员发送抛出 `WKIMError.code == 3`，黑名单发送返回错误码 `4`。策略由服务端决定；错误或超时后先核对成员关系及发送结果，再决定是否重试。SENDACK 不是用户已读确认；重连不会自动补拉离线历史。

**服务端版本要求：**跨入口成员变更验收使用包含修复的服务端源码 [`2a295e0d9881ef5356728a85d56b052c4b0d9c86`](https://github.com/WuKongIM/WuKongIM/commit/2a295e0d9881ef5356728a85d56b052c4b0d9c86)。旧服务端可能在其他节点变更成员后仍使用过期投递缓存；只升级 Python 包不能修复这一行为。该记录验证的是修复源码，不代表旧服务端安装包已包含修复。

[群聊正式包验证记录](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/4d39f9e43265f88415f2fe01fc5418182e405c52/docs/GROUP_VALIDATION.md)独立记录三节点 WSS、4 个 Python/JS 客户端、2 个群的 13 个阶段：成员投递、群隔离、加入/移除/重新加入、非成员与黑名单拒绝，以及四个客户端重连后的成员与权限保持。Python 3.11 和 3.14 的 PyPI 消费环境还实际运行群聊 CLI。每个阶段对不应收到消息的客户端观察 1 秒；这是功能验收，不是大群容量或恰好一次投递保证。独立 CI 的候选 wheel 记录与 PyPI 下载记录分开保留。

验收在 12 个 Slot 完成初始 Leader 收敛后才登录客户端，并记录前后拓扑。启动期间的 Leader 调整曾导致在线路由暂时缺失，诊断与失败记录保留在[问题 #7](https://github.com/WuKongIM/WuKongEasySDK-Python/issues/7)。现有服务端在 Slot Leader 变化后依靠下一次有效客户端活动重建在线路由；本次验证不保证 Slot Leader 切换期间连续在线投递，也不自动补拉缺失消息。


### en source

[Immutable original](https://github.com/WuKongIM/WuKongIM/blob/96dc9758f421db39ae5f0d0153c3911be7c10c4c/docs-site/content/docs/sdk/easy/python/getting-started.en.mdx)

[WuKongEasySDK-Python](https://github.com/WuKongIM/WuKongEasySDK-Python) follows JS `v2.0.4` and uses Python 3.11+, `asyncio`, and `websockets` for WebSocket JSON-RPC CONNECT, online messaging, automatic RECVACK, heartbeat, reconnect, and custom events.

#### 6. WSS and validation scope

WSS verifies certificate chains and hostnames by default, with a TLS 1.2 minimum. Use `WKIMOptions(ca_file="/path/ca.pem")` for a private CA; the interactive example reads `WKIM_CA_FILE`. There is no verification bypass or automatic system-proxy discovery; supply the reachable Gateway/proxy URL directly.

Logging is off by default. `WKIMOptions(debug_logging=True)` enables only fixed lifecycle metadata, excluding Tokens, Payloads, URLs, raw frames, peer response text, and underlying exception objects.

PyPI `0.1.0` was published from `ec2c62c73eca29be99ac15ba76ff7466c13617d5`. The public wheel and sdist SHA-256 values match the publish workflow artifacts. Clean PyPI installations on Python `3.11.12` / websockets `15.0.1` and Python `3.14.7` / websockets `17.1` each passed 64 tests and real Python/Python and JS `2.0.4` bidirectional messaging, Ping, manual reconnect, invalid-Token rejection, and online cleanup against WuKongIM `0348c0539bbee420a859439695acdac911afa854`, a Token-authenticated 256-hash-slot single-node cluster. JS was built from pinned source. Independent WS/WSS tests cover automatic retry, cancellation, queue bounds, and certificate rejection. See the [PyPI package validation record](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/90018a9ac53bdef4cb15dadb169ff835cbfcb2a6/docs/PYPI_VALIDATION.md) for exact hashes, installation receipts, versions, and reproduction commands.

A separate [three-node WSS acceptance workflow](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/a9be49e69f4b7155a9a6244ef63f9cb6baf00cb9/docs/CLUSTER_ACCEPTANCE.md) installs the pinned PyPI version and connects through private-CA TLS proxies to three real nodes. It covers cross-node messaging, lost SENDACKs, transport recovery, node restart, Token rotation and cleanup. CI runs a short acceptance; manual runs can select 30 or 60 minutes. Each receipt identifies the package, server, JS and harness separately.

The same PyPI `0.1.0` then completed a **30-minute WSS acceptance** against WuKongIM `e7ef61ba702e045648b9fa535f051e5b2ee4a1db` on a local three-node cluster with 256 hash slots, 12 physical Slots and three Slot replicas, matching **35,380 SENDACK/RECV pairs**. Six transport/ACK-loss faults, an ingress node restart and Token rotation on all three nodes passed. No duplicate callbacks were observed; connection/task counts remained stable, with zero owned processes, connections or extra tasks after cleanup. The run uses explicitly configured fault-test deadlines and reconnect limits. See the [three-node WSS validation record](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/a9be49e69f4b7155a9a6244ef63f9cb6baf00cb9/docs/CLUSTER_VALIDATION.md) for versions, resource samples and raw JSON; this is not a production-capacity or exactly-once-delivery guarantee.

Offline sync, conversations, unread counts, and push require the full SDK. Validate your actual WSS proxy, Token rotation, duplicate handling, and capacity, or return to the [EasySDK overview](https://docs.githubim.com/en/sdk/easy) and [official examples](https://docs.githubim.com/en/sdk/easy/examples).

#### 7. Online group messaging and permissions

Keep the installed package at `wukong-easy-sdk==0.1.0`. Groups use the same connection, `WKIMChannelType.GROUP` (`2`) and `WKIMEvent.MESSAGE`; there is no client subscription call:

```python
await im.send("team-chat", WKIMChannelType.GROUP, {"type": 1, "content": "Hello, group"})
```

**A trusted backend first creates the group, adds members, and supplies device-category `2` Tokens.** Execute this Product HTTP request only on the trusted server; do not expose management access to untrusted clients. This example adds Alice and Bob and explicitly rejects nonmember sends:

```http
POST /channel
Content-Type: application/json

{"channel_id":"team-chat","channel_type":2,"allow_stranger":0,"reset":1,"subscribers":["alice","bob"]}
```

`reset:1` replaces existing membership, so use it only for a new example group. For subsequent changes, the backend uses `/channel/subscriber_add` or `/channel/subscriber_remove` with `channel_id`, `channel_type:2` and `subscribers`. Blacklist changes use `/channel/blacklist_add` or `/channel/blacklist_remove` with `uids` instead of `subscribers`.

Download the pinned source containing the new group example; the original `v0.1.0` tag predates this file. Example source and the installed PyPI package are pinned separately:

```sh
git clone https://github.com/WuKongIM/WuKongEasySDK-Python.git WuKongEasySDK-Python-group
git -C WuKongEasySDK-Python-group checkout --detach 527f37c876326e7ad3cc48c89828c4c3ffed09fc
export WKIM_UID=alice
export WKIM_GROUP=team-chat
# Supply WKIM_TOKEN and WKIM_URL securely; set WKIM_CA_FILE for a private CA.
python WuKongEasySDK-Python-group/examples/group_chat.py
```

Use Bob's UID and Token with the same group ID in another terminal. Once both members are online, type messages or `/quit` to exit. The example displays message content and numeric errors. Under the policy above, nonmember sends raise `WKIMError.code == 3`; blacklist rejection uses code `4`. Server policy is authoritative. Check membership and reconcile uncertain send outcomes before retrying. SENDACK is not a read receipt; reconnection does not fetch missed history.

**Server requirement:** cross-ingress membership acceptance uses fixed server source [`2a295e0d9881ef5356728a85d56b052c4b0d9c86`](https://github.com/WuKongIM/WuKongIM/commit/2a295e0d9881ef5356728a85d56b052c4b0d9c86). Older servers can retain stale recipients after membership changes through another node; upgrading the Python package cannot correct that server behavior. This record validates fixed source and does not imply that older server installation packages contain the fix.

The [group package validation record](https://github.com/WuKongIM/WuKongEasySDK-Python/blob/4d39f9e43265f88415f2fe01fc5418182e405c52/docs/GROUP_VALIDATION.md) separately covers three-node WSS, four Python/JS clients, two groups and 13 phases: member fanout, channel isolation, add/remove/readd, nonmember/blacklist rejection, and membership/permissions after all four clients reconnect. Python 3.11 and 3.14 PyPI consumers also execute the real group CLI. Each phase observes excluded clients for one second. This is functional acceptance, not large-group capacity or an exactly-once guarantee. Independent CI candidate-wheel receipts remain separate from PyPI download evidence.

Clients log in after all 12 Slots finish initial preferred-leader convergence, with both topology snapshots recorded. Startup leader changes previously left temporary gaps in online routes; [issue #7](https://github.com/WuKongIM/WuKongEasySDK-Python/issues/7) retains the diagnosis and failed receipts. The existing server reconstructs presence after an authority change from the next valid client activity. This validation does not guarantee uninterrupted online delivery during Slot leader migration or automatically recover missing messages.
