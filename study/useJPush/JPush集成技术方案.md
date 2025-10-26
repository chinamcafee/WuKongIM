# WuKongIM 集成 JPush（极光推送）技术方案

## 文档版本

- **版本**: 1.0
- **创建日期**: 2025-10-18
- **适用系统**: WuKongIM v2.x

---

## 一、方案概述

### 1.1 业务背景

在移动端IM应用中，当用户设备离线时，无法通过长连接接收消息。为保证消息的及时送达，需要集成第三方推送服务（如JPush极光推送）来实现离线消息推送功能。

### 1.2 技术目标

- 检测移动端设备离线状态
- 将离线消息通过JPush推送到移动设备
- 支持iOS和Android双平台推送
- 保持现有Webhook机制的兼容性
- 支持灵活的推送配置和扩展

### 1.3 集成方案选择

WuKongIM提供了两种JPush集成方案：

#### **方案A：Webhook集成（推荐）**
- **优点**：松耦合、易于维护、不修改核心代码、支持多种推送服务切换
- **缺点**：需要额外的中间服务
- **适用场景**：生产环境、需要灵活配置的场景

#### **方案B：内置集成**
- **优点**：无需额外服务、性能更好、配置简单
- **缺点**：紧耦合、修改核心代码、扩展性较差
- **适用场景**：简单场景、对性能要求极高的场景

**本文档重点介绍方案A（Webhook集成），并提供方案B的实现思路。**

---

## 二、系统架构分析

### 2.1 WuKongIM消息推送架构

```
[消息发送] → [频道分发] → [在线/离线判断] → [推送处理]
                                              ├─ [在线推送] → 长连接投递
                                              └─ [离线推送] → Webhook通知
```

### 2.2 离线消息处理流程

```
1. 消息到达频道分发器 (internal/channel/handler/event_distribute.go)
   ↓
2. 判断订阅者在线状态，筛选离线用户
   ↓
3. 生成 EventPushOffline 事件，包含离线用户列表 (Event.OfflineUsers)
   ↓
4. 推送处理器接收事件 (internal/pusher/handler/event_pushoffline.go)
   ↓
5. 通过 Webhook 发送离线消息通知 (internal/webhook/webhook.go)
   ↓
6. 第三方服务接收通知，调用 JPush API 进行推送
```

### 2.3 核心代码位置

| 模块 | 文件路径 | 功能说明 |
|------|---------|---------|
| 消息分发 | `internal/channel/handler/event_distribute.go` | 判断用户在线/离线状态，生成推送事件 |
| 离线推送处理 | `internal/pusher/handler/event_pushoffline.go` | 处理离线推送事件 |
| Webhook通知 | `internal/webhook/webhook.go` | 发送Webhook通知到第三方服务 |
| 配置管理 | `internal/options/options.go` | 系统配置项定义 |
| 事件定义 | `internal/types/webhook.go` | 事件类型和数据结构定义 |
| 事件总线 | `internal/eventbus/event.go` | 事件结构定义，包含OfflineUsers字段 |

---

## 三、方案A：Webhook集成（推荐）

### 3.1 架构设计

```
WuKongIM                          中间服务                      JPush
   │                                 │                           │
   │  1. 检测离线消息                 │                           │
   ├────────────────────────────────>│                           │
   │  POST /webhook?event=msg.offline│                           │
   │  Body: {                        │                           │
   │    message_id,                  │                           │
   │    from_uid,                    │                           │
   │    to_uids: [...],              │                           │
   │    payload,                     │                           │
   │    ...                          │                           │
   │  }                              │                           │
   │                                 │  2. 调用JPush API          │
   │                                 ├──────────────────────────>│
   │                                 │  POST /v3/push            │
   │                                 │  Body: {                  │
   │                                 │    platform: ["ios"],     │
   │                                 │    audience: {            │
   │                                 │      registration_id: []  │
   │                                 │    },                     │
   │                                 │    notification: {...}    │
   │                                 │  }                        │
   │                                 │                           │
   │                                 │<─────────────────────────│
   │                                 │  3. 返回推送结果           │
   │<────────────────────────────────│                           │
   │  HTTP 200 OK                    │                           │
```

### 3.2 实现步骤

#### 步骤1：配置WuKongIM的Webhook

**修改配置文件** (如 `wk.yaml` 或 `cluster1.yaml`)：

```yaml
webhook:
  httpAddr: "http://your-push-service:8080/webhook"  # 中间推送服务地址
  msgNotifyEventPushInterval: 500ms                   # 推送间隔
  msgNotifyEventCountPerPush: 100                     # 每次推送消息数量
  msgNotifyEventRetryMaxCount: 5                      # 最大重试次数
  focusEvents:                                        # 关注的事件类型
    - "msg.offline"                                   # 离线消息事件
```

**相关源码位置**：`internal/options/options.go:127-134`

```go
Webhook struct {
    HTTPAddr                    string        // webhook的http地址
    GRPCAddr                    string        // webhook的grpc地址
    MsgNotifyEventPushInterval  time.Duration // 消息通知事件推送间隔
    MsgNotifyEventCountPerPush  int           // 每次推送消息数量限制
    MsgNotifyEventRetryMaxCount int           // 推送失败最大重试次数
    FocusEvents                 []string      // 关注的通知事件
}
```

#### 步骤2：开发中间推送服务

**服务职责**：
1. 接收WuKongIM的Webhook通知
2. 解析离线消息数据
3. 查询用户的设备Token（JPush Registration ID）
4. 构造JPush推送请求
5. 调用JPush REST API
6. 返回推送结果

**接口定义**：

```go
// Webhook接收接口
POST /webhook?event=msg.offline

// 请求体结构（WuKongIM发送）
{
  "header": {
    "red_dot": 1,      // 是否显示红点
    "sync_once": 0,    // 是否只同步一次
    "no_persist": 0    // 是否持久化
  },
  "setting": 0,
  "client_msg_no": "xxx",
  "message_id": 123456,
  "message_id_str": "123456",
  "message_seq": 100,
  "from_uid": "user001",
  "channel_id": "user002",
  "channel_type": 1,     // 1:个人 2:群组
  "topic": "",
  "expire": 0,
  "timestamp": 1729234567,
  "payload": "SGVsbG8gV29ybGQ=",  // Base64编码的消息内容
  "to_uids": ["user002", "user003"],  // 离线用户列表
  "compress": "",                      // 压缩方式（如果有）
  "source_id": 1                       // 来源节点ID
}
```

**中间服务示例代码**（Go语言）：

```go
package main

import (
    "encoding/base64"
    "encoding/json"
    "fmt"
    "net/http"
    "github.com/gin-gonic/gin"
)

// WuKongIM离线消息通知结构
type OfflineMessageNotify struct {
    Header struct {
        RedDot    int `json:"red_dot"`
        SyncOnce  int `json:"sync_once"`
        NoPersist int `json:"no_persist"`
    } `json:"header"`
    MessageID    int64    `json:"message_id"`
    MessageSeq   int64    `json:"message_seq"`
    FromUID      string   `json:"from_uid"`
    ChannelID    string   `json:"channel_id"`
    ChannelType  uint8    `json:"channel_type"`
    Payload      string   `json:"payload"`      // Base64编码
    ToUIDs       []string `json:"to_uids"`      // 离线用户列表
    Timestamp    int32    `json:"timestamp"`
}

// JPush推送请求结构
type JPushRequest struct {
    Platform     []string              `json:"platform"`
    Audience     map[string][]string   `json:"audience"`
    Notification JPushNotification     `json:"notification"`
    Options      JPushOptions          `json:"options"`
}

type JPushNotification struct {
    Alert string            `json:"alert"`
    IOS   *JPushIOS         `json:"ios,omitempty"`
    Android *JPushAndroid   `json:"android,omitempty"`
}

type JPushIOS struct {
    Alert            string `json:"alert"`
    Sound            string `json:"sound"`
    Badge            int    `json:"badge"`
    ContentAvailable bool   `json:"content-available"`
    MutableContent   bool   `json:"mutable-content"`
}

type JPushAndroid struct {
    Alert  string `json:"alert"`
    Title  string `json:"title"`
}

type JPushOptions struct {
    ApnsProduction bool `json:"apns_production"` // iOS生产环境
}

func main() {
    r := gin.Default()

    r.POST("/webhook", handleWebhook)

    r.Run(":8080")
}

func handleWebhook(c *gin.Context) {
    event := c.Query("event")

    // 只处理离线消息事件
    if event != "msg.offline" {
        c.JSON(http.StatusOK, gin.H{"status": "ignored"})
        return
    }

    var notify OfflineMessageNotify
    if err := c.ShouldBindJSON(&notify); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // 解码消息内容
    payload, _ := base64.StdEncoding.DecodeString(notify.Payload)

    // 解析消息内容（假设是JSON格式）
    var messageContent map[string]interface{}
    json.Unmarshal(payload, &messageContent)

    // 获取消息文本
    messageText := fmt.Sprintf("收到新消息")
    if content, ok := messageContent["content"].(string); ok {
        messageText = content
    }

    // 为每个离线用户推送
    for _, toUID := range notify.ToUIDs {
        // 1. 从数据库查询用户的JPush Registration ID
        registrationIDs := getUserJPushTokens(toUID)

        if len(registrationIDs) == 0 {
            continue
        }

        // 2. 构造JPush请求
        pushReq := JPushRequest{
            Platform: []string{"ios", "android"},
            Audience: map[string][]string{
                "registration_id": registrationIDs,
            },
            Notification: JPushNotification{
                Alert: messageText,
                IOS: &JPushIOS{
                    Alert:            messageText,
                    Sound:            "default",
                    Badge:            1,
                    ContentAvailable: true,
                    MutableContent:   true,
                },
                Android: &JPushAndroid{
                    Alert: messageText,
                    Title: "新消息",
                },
            },
            Options: JPushOptions{
                ApnsProduction: true, // 生产环境设为true
            },
        }

        // 3. 调用JPush API
        err := sendJPush(pushReq)
        if err != nil {
            fmt.Printf("JPush发送失败: %v\n", err)
        }
    }

    c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// 从数据库查询用户的JPush设备Token
func getUserJPushTokens(uid string) []string {
    // TODO: 实现数据库查询逻辑
    // 返回该用户所有设备的JPush Registration ID
    return []string{"registration_id_1", "registration_id_2"}
}

// 调用JPush REST API
func sendJPush(req JPushRequest) error {
    // TODO: 实现JPush API调用
    // 使用 JPush Go SDK 或直接调用REST API
    // API文档: https://docs.jiguang.cn/jpush/server/push/rest_api_v3_push

    appKey := "your-jpush-app-key"
    masterSecret := "your-jpush-master-secret"

    // 示例代码（需要引入JPush Go SDK）
    // client := jpush.NewClient(appKey, masterSecret)
    // result, err := client.Push(req)

    return nil
}
```

**用户设备Token管理**：

在移动端App中，需要在登录后上报JPush Registration ID：

```go
// API接口：绑定用户和设备Token
POST /api/user/bind-push-token

{
  "uid": "user001",
  "platform": "ios",  // ios 或 android
  "registration_id": "jpush-registration-id",
  "device_id": "device-uuid"
}
```

数据库表设计：

```sql
CREATE TABLE user_push_tokens (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  uid VARCHAR(64) NOT NULL,
  platform VARCHAR(16) NOT NULL,  -- ios, android
  registration_id VARCHAR(256) NOT NULL,
  device_id VARCHAR(128),
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_uid (uid)
);
```

#### 步骤3：部署和测试

1. **部署中间服务**
2. **配置WuKongIM的Webhook地址**
3. **测试流程**：
   - 用户A离线
   - 用户B给用户A发消息
   - WuKongIM检测到用户A离线，发送Webhook通知
   - 中间服务接收通知，调用JPush推送
   - 用户A的设备收到推送通知

### 3.3 Webhook事件数据结构

**WuKongIM发送的事件数据** (`internal/types/webhook.go:15-21`)：

```go
type MessageOfflineNotify struct {
    MessageResp
    ToUids          []string `json:"to_uids"`             // 离线用户列表
    Compress        string   `json:"compress,omitempty"`  // 压缩方式
    CompresssToUids []byte   `json:"compress_to_uids,omitempty"` // 压缩后的用户列表
    SourceId        int64    `json:"source_id,omitempty"` // 来源节点ID
}
```

**离线消息通知触发位置**：`internal/webhook/webhook.go:228-289`

---

## 四、方案B：内置集成

### 4.1 架构设计

在WuKongIM内部直接集成JPush SDK，在离线推送处理器中直接调用JPush API。

### 4.2 需要修改的代码

#### 修改1：添加JPush配置

**文件**: `internal/options/options.go`

**位置**: 在 `Webhook struct` 后添加

```go
// 在 Options 结构体中添加（约第134行后）
JPush struct {
    Enabled       bool   // 是否启用JPush推送
    AppKey        string // JPush AppKey
    MasterSecret  string // JPush MasterSecret
    ApnsProduction bool  // iOS生产环境标识
} `yaml:"jpush"`
```

**配置文件示例** (`wk.yaml`):

```yaml
jpush:
  enabled: true
  appKey: "your-jpush-app-key"
  masterSecret: "your-jpush-master-secret"
  apnsProduction: true  # 生产环境设为true，开发环境设为false
```

#### 修改2：创建JPush推送管理器

**新建文件**: `internal/pusher/jpush/manager.go`

```go
package jpush

import (
    "context"
    "encoding/json"
    "github.com/WuKongIM/WuKongIM/internal/eventbus"
    "github.com/WuKongIM/WuKongIM/internal/options"
    "github.com/WuKongIM/WuKongIM/internal/service"
    "github.com/WuKongIM/WuKongIM/pkg/wklog"
    wkproto "github.com/WuKongIM/WuKongIMGoProto"
    "go.uber.org/zap"
    // 引入JPush Go SDK
    // jpushclient "github.com/ylywyn/jpush-api-go-client"
)

type Manager struct {
    wklog.Log
    enabled bool
    appKey  string
    secret  string
    apnsProduction bool
    // jpushClient *jpushclient.PushClient
}

func NewManager() *Manager {
    m := &Manager{
        Log:            wklog.NewWKLog("JPushManager"),
        enabled:        options.G.JPush.Enabled,
        appKey:         options.G.JPush.AppKey,
        secret:         options.G.JPush.MasterSecret,
        apnsProduction: options.G.JPush.ApnsProduction,
    }

    if m.enabled {
        // 初始化JPush客户端
        // m.jpushClient = jpushclient.NewPushClient(m.secret, m.appKey)
        m.Info("JPush推送已启用", zap.String("appKey", m.appKey))
    }

    return m
}

func (m *Manager) Enabled() bool {
    return m.enabled
}

// Push 推送离线消息
func (m *Manager) Push(e *eventbus.Event) {
    if !m.enabled {
        return
    }

    sendPacket, ok := e.Frame.(*wkproto.SendPacket)
    if !ok {
        return
    }

    // 遍历所有离线用户
    for _, toUID := range e.OfflineUsers {
        // 避免发送者收到推送
        if toUID == e.Conn.Uid {
            continue
        }

        // 查询用户的设备Token
        registrationIDs, err := m.getUserPushTokens(toUID)
        if err != nil {
            m.Error("查询用户推送Token失败", zap.String("uid", toUID), zap.Error(err))
            continue
        }

        if len(registrationIDs) == 0 {
            m.Debug("用户没有推送Token", zap.String("uid", toUID))
            continue
        }

        // 解析消息内容
        var messageContent map[string]interface{}
        json.Unmarshal(sendPacket.Payload, &messageContent)

        alertText := "您有新消息"
        if content, ok := messageContent["content"].(string); ok {
            alertText = content
        }

        // 调用JPush推送
        err = m.sendPush(registrationIDs, alertText)
        if err != nil {
            m.Error("JPush推送失败", zap.String("uid", toUID), zap.Error(err))
        } else {
            m.Debug("JPush推送成功", zap.String("uid", toUID))
        }
    }
}

// getUserPushTokens 查询用户的推送Token
func (m *Manager) getUserPushTokens(uid string) ([]string, error) {
    // TODO: 从数据库或缓存查询用户的JPush Registration ID
    // 可以通过 service.Store 或自定义数据源获取
    return []string{}, nil
}

// sendPush 发送JPush推送
func (m *Manager) sendPush(registrationIDs []string, alertText string) error {
    // TODO: 调用JPush Go SDK发送推送
    // 示例代码：
    /*
    payload := jpushclient.NewPushPayLoad()
    payload.SetPlatform(jpushclient.AllPlatform())
    payload.SetAudience(jpushclient.RegistrationId(registrationIDs))

    // iOS通知
    iosNotice := jpushclient.NewIOSNotice()
    iosNotice.SetAlert(alertText)
    iosNotice.SetSound("default")
    iosNotice.SetBadge(1)
    iosNotice.SetContentAvailable(true)

    // Android通知
    androidNotice := jpushclient.NewAndroidNotice()
    androidNotice.SetAlert(alertText)
    androidNotice.SetTitle("新消息")

    payload.SetNotification(&jpushclient.Notification{
        IOS:     iosNotice,
        Android: androidNotice,
    })

    // 设置选项
    payload.SetOptions(&jpushclient.Options{
        ApnsProduction: m.apnsProduction,
    })

    result, err := m.jpushClient.Send(payload)
    if err != nil {
        return err
    }

    m.Info("JPush推送结果", zap.Any("result", result))
    */

    return nil
}
```

#### 修改3：在服务层注册JPush管理器

**文件**: `internal/service/common.go`

**添加**:

```go
var JPushManager *jpush.Manager

func init() {
    // 在现有初始化代码后添加
    JPushManager = jpush.NewManager()
}
```

#### 修改4：在离线推送处理中调用JPush

**文件**: `internal/pusher/handler/event_pushoffline.go:9-22`

**修改后的代码**:

```go
func (h *Handler) pushOffline(ctx *eventbus.PushContext) {
    for _, e := range ctx.Events {

        // ========== 1. 原有逻辑：AI推送处理 ==========
        for _, toUid := range e.OfflineUsers {
            fromUid := e.Conn.Uid
            if fromUid != toUid && h.isAI(toUid) && !e.Frame.GetsyncOnce() && !options.G.IsSystemUid(fromUid) {
                h.processAIPush(toUid, e)
            }
        }

        // ========== 2. 新增：JPush推送 ==========
        if service.JPushManager != nil && service.JPushManager.Enabled() {
            service.JPushManager.Push(e)
        }
    }

    // ========== 3. 原有逻辑：Webhook通知 ==========
    service.Webhook.NotifyOfflineMsg(ctx.Events)
}
```

**业务含义说明**：
- 当检测到用户离线时，先处理AI推送（如果目标用户是AI机器人）
- 然后调用JPush管理器进行离线推送（如果启用了JPush）
- 最后保持原有的Webhook通知机制（保证向后兼容）

#### 修改5：提供用户设备Token绑定API

**文件**: `internal/api/user.go`

**添加接口**:

```go
// BindPushToken 绑定用户推送Token
func (u *User) bindPushToken(c *gin.Context) {
    var req struct {
        UID            string `json:"uid" binding:"required"`
        Platform       string `json:"platform" binding:"required"` // ios, android
        RegistrationID string `json:"registration_id" binding:"required"`
        DeviceID       string `json:"device_id"`
    }

    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // TODO: 保存到数据库
    // 可以扩展 wkdb 或使用独立的存储

    c.JSON(http.StatusOK, gin.H{"status": "success"})
}
```

**注册路由** (在 `internal/api/route.go` 中):

```go
func (r *Route) route(api *gin.RouterGroup) {
    // ... 现有路由

    // 用户相关API
    user := api.Group("/user")
    {
        user.POST("/bind-push-token", r.s.user.bindPushToken)
    }
}
```

### 4.3 依赖管理

**添加JPush Go SDK依赖**:

```bash
go get github.com/ylywyn/jpush-api-go-client
```

**更新 `go.mod`**:

```
require (
    github.com/ylywyn/jpush-api-go-client v0.0.0-20190906031852-8c4466c6e369
)
```

---

## 五、代码修改清单

### 5.1 方案A（Webhook集成）- 无需修改WuKongIM源码

| 序号 | 操作 | 说明 |
|------|------|------|
| 1 | 修改配置文件 | 配置Webhook地址和相关参数 |
| 2 | 开发中间推送服务 | 新建独立的推送服务项目 |
| 3 | 部署中间服务 | 部署和运行推送服务 |

### 5.2 方案B（内置集成）- 需修改源码

| 序号 | 文件路径 | 修改类型 | 行数位置 | 修改内容 | 业务含义 |
|------|---------|---------|---------|---------|---------|
| 1 | `internal/options/options.go` | 新增 | ~134行后 | 添加JPush配置结构体 | 支持JPush配置项（AppKey、MasterSecret等） |
| 2 | `internal/pusher/jpush/manager.go` | 新建 | - | 创建JPush管理器 | 封装JPush推送逻辑，管理推送客户端 |
| 3 | `internal/service/common.go` | 新增 | init函数中 | 注册JPushManager实例 | 初始化JPush管理器，使其可全局访问 |
| 4 | `internal/pusher/handler/event_pushoffline.go` | 修改 | 9-22行 | 在pushOffline函数中调用JPush | 在离线推送处理流程中集成JPush推送 |
| 5 | `internal/api/user.go` | 新增 | - | 添加bindPushToken接口 | 提供用户设备Token绑定API |
| 6 | `internal/api/route.go` | 修改 | route函数中 | 注册用户Token绑定路由 | 暴露Token绑定接口给客户端 |
| 7 | `go.mod` | 修改 | - | 添加JPush SDK依赖 | 引入JPush Go SDK |

---

## 六、技术方案对比

| 对比项 | 方案A（Webhook集成） | 方案B（内置集成） |
|--------|---------------------|------------------|
| **代码耦合度** | 低（松耦合） | 高（紧耦合） |
| **代码修改量** | 无需修改核心代码 | 需修改6-7个文件 |
| **部署复杂度** | 需要额外服务 | 无需额外服务 |
| **维护成本** | 低（独立服务） | 中（核心代码） |
| **性能开销** | 多一次HTTP请求 | 直接调用，性能更好 |
| **扩展性** | 易于切换推送服务 | 切换需修改代码 |
| **推送服务切换** | 只需修改中间服务 | 需修改核心代码 |
| **测试难度** | 易于测试和调试 | 需要重新编译WuKongIM |
| **生产环境推荐** | ★★★★★ | ★★★ |

---

## 七、数据库设计

### 7.1 用户推送Token表

```sql
CREATE TABLE `user_push_tokens` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主键ID',
  `uid` VARCHAR(64) NOT NULL COMMENT '用户ID',
  `platform` VARCHAR(16) NOT NULL COMMENT '平台类型：ios, android',
  `registration_id` VARCHAR(256) NOT NULL COMMENT 'JPush Registration ID',
  `device_id` VARCHAR(128) DEFAULT NULL COMMENT '设备唯一标识',
  `app_version` VARCHAR(32) DEFAULT NULL COMMENT 'App版本号',
  `status` TINYINT DEFAULT 1 COMMENT '状态：1-有效 0-无效',
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_device` (`device_id`),
  KEY `idx_uid` (`uid`),
  KEY `idx_registration_id` (`registration_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户推送Token表';
```

### 7.2 推送记录表（可选，用于统计和问题排查）

```sql
CREATE TABLE `push_logs` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主键ID',
  `uid` VARCHAR(64) NOT NULL COMMENT '用户ID',
  `message_id` BIGINT NOT NULL COMMENT '消息ID',
  `from_uid` VARCHAR(64) NOT NULL COMMENT '发送者ID',
  `platform` VARCHAR(16) NOT NULL COMMENT '平台类型',
  `registration_id` VARCHAR(256) NOT NULL COMMENT 'JPush Registration ID',
  `push_content` TEXT COMMENT '推送内容',
  `push_status` TINYINT DEFAULT 0 COMMENT '推送状态：0-待推送 1-成功 2-失败',
  `push_result` TEXT COMMENT '推送结果（JPush返回）',
  `error_msg` TEXT COMMENT '错误信息',
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  PRIMARY KEY (`id`),
  KEY `idx_uid` (`uid`),
  KEY `idx_message_id` (`message_id`),
  KEY `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='推送记录表';
```

---

## 八、移动端集成

### 8.1 iOS端集成

```swift
// 1. 引入JPush SDK (Podfile)
pod 'JPush'

// 2. 初始化JPush
import JPUSHService

func application(_ application: UIApplication,
                 didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {

    // JPush初始化
    let apsForProduction = true // 生产环境设为true
    JPUSHService.setup(withOption: launchOptions,
                      appKey: "your-jpush-app-key",
                      channel: "App Store",
                      apsForProduction: apsForProduction)

    // 注册远程通知
    JPUSHService.register(forRemoteNotificationTypes:
        (UNAuthorizationOptions.alert.rawValue |
         UNAuthorizationOptions.badge.rawValue |
         UNAuthorizationOptions.sound.rawValue),
        categories: nil)

    return true
}

// 3. 获取Registration ID并上报
func application(_ application: UIApplication,
                 didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
    JPUSHService.registerDeviceToken(deviceToken)
}

// 监听Registration ID获取成功
NotificationCenter.default.addObserver(forName: NSNotification.Name.jpfNetworkDidRegister,
                                       object: nil,
                                       queue: nil) { notification in
    let registrationID = JPUSHService.registrationID()

    // 上报到WuKongIM服务器
    self.uploadRegistrationID(registrationID)
}

// 上报Registration ID到服务器
func uploadRegistrationID(_ registrationID: String) {
    let url = URL(string: "http://your-server/api/user/bind-push-token")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")

    let params: [String: Any] = [
        "uid": currentUserID,
        "platform": "ios",
        "registration_id": registrationID,
        "device_id": UIDevice.current.identifierForVendor?.uuidString ?? ""
    ]

    request.httpBody = try? JSONSerialization.data(withJSONObject: params)

    URLSession.shared.dataTask(with: request).resume()
}
```

### 8.2 Android端集成

```java
// 1. 引入JPush SDK (build.gradle)
dependencies {
    implementation 'cn.jiguang.sdk:jpush:4.8.0'
    implementation 'cn.jiguang.sdk:jcore:3.3.0'
}

// 2. 在Application中初始化
public class MyApplication extends Application {
    @Override
    public void onCreate() {
        super.onCreate();

        // JPush初始化
        JPushInterface.setDebugMode(false);
        JPushInterface.init(this);

        // 获取Registration ID
        String registrationID = JPushInterface.getRegistrationID(this);
        if (!TextUtils.isEmpty(registrationID)) {
            uploadRegistrationID(registrationID);
        }
    }
}

// 3. 监听Registration ID变化
public class MyJPushMessageReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context context, Intent intent) {
        String action = intent.getAction();

        if (JPushInterface.ACTION_REGISTRATION_ID.equals(action)) {
            String registrationID = intent.getStringExtra(JPushInterface.EXTRA_REGISTRATION_ID);
            uploadRegistrationID(registrationID);
        }
    }
}

// 4. 上报Registration ID
private void uploadRegistrationID(String registrationID) {
    String url = "http://your-server/api/user/bind-push-token";

    JSONObject params = new JSONObject();
    params.put("uid", currentUserID);
    params.put("platform", "android");
    params.put("registration_id", registrationID);
    params.put("device_id", getDeviceId());

    // 使用OkHttp或其他网络库上传
}
```

---

## 九、测试方案

### 9.1 单元测试

```go
// 文件：internal/pusher/jpush/manager_test.go
package jpush

import (
    "testing"
    "github.com/WuKongIM/WuKongIM/internal/eventbus"
    wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

func TestJPushManager_Push(t *testing.T) {
    // 创建测试事件
    event := &eventbus.Event{
        OfflineUsers: []string{"user001", "user002"},
        Conn: &eventbus.Conn{
            Uid: "sender001",
        },
        Frame: &wkproto.SendPacket{
            Payload: []byte(`{"type":"text","content":"Hello"}`),
        },
    }

    // 创建JPush管理器
    manager := NewManager()

    // 执行推送
    manager.Push(event)

    // 验证推送结果
    // ...
}
```

### 9.2 集成测试

**测试步骤**：

1. **准备环境**
   - 启动WuKongIM服务
   - 启动推送中间服务（方案A）
   - 配置JPush AppKey和MasterSecret

2. **测试离线推送**
   ```bash
   # 1. 用户A登录并绑定设备Token
   curl -X POST http://localhost:5001/api/user/bind-push-token \
     -H "Content-Type: application/json" \
     -d '{
       "uid": "userA",
       "platform": "ios",
       "registration_id": "test-registration-id",
       "device_id": "device-001"
     }'

   # 2. 用户A断开连接（模拟离线）

   # 3. 用户B给用户A发送消息
   curl -X POST http://localhost:5001/message/send \
     -H "Content-Type: application/json" \
     -d '{
       "from_uid": "userB",
       "channel_id": "userA",
       "channel_type": 1,
       "payload": "SGVsbG8gV29ybGQ="
     }'

   # 4. 检查推送日志
   # - WuKongIM日志应显示 Webhook调用（方案A）或 JPush调用（方案B）
   # - 中间服务日志应显示接收到Webhook并调用JPush（方案A）
   # - JPush控制台应显示推送记录
   ```

3. **验证推送到达**
   - iOS设备应收到系统推送通知
   - Android设备应收到系统推送通知

### 9.3 压力测试

```bash
# 使用 WuKongIM 自带的压力测试工具
# 测试大量离线用户的推送性能

# 配置：10000个离线用户，每秒1000条消息
./wukongim stress --offline-users=10000 --msg-rate=1000
```

**监控指标**：
- Webhook调用成功率
- JPush API调用成功率
- 推送延迟（从消息发送到推送送达的时间）
- 推送到达率

---

## 十、运维监控

### 10.1 监控指标

| 指标名称 | 说明 | 告警阈值 |
|---------|------|---------|
| `jpush_push_total` | JPush推送总数 | - |
| `jpush_push_success` | JPush推送成功数 | - |
| `jpush_push_failed` | JPush推送失败数 | > 100/min |
| `jpush_push_latency` | JPush推送延迟（ms） | > 1000ms |
| `webhook_call_total` | Webhook调用总数 | - |
| `webhook_call_failed` | Webhook调用失败数 | > 50/min |

### 10.2 Prometheus指标采集

```go
// 在 internal/pusher/jpush/manager.go 中添加
import (
    "github.com/prometheus/client_golang/prometheus"
)

var (
    jpushPushTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "jpush_push_total",
            Help: "JPush推送总数",
        },
        []string{"status"}, // success, failed
    )

    jpushPushLatency = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "jpush_push_latency_milliseconds",
            Help:    "JPush推送延迟（毫秒）",
            Buckets: []float64{10, 50, 100, 500, 1000, 5000},
        },
    )
)

func init() {
    prometheus.MustRegister(jpushPushTotal)
    prometheus.MustRegister(jpushPushLatency)
}

// 在推送时记录指标
func (m *Manager) sendPush(registrationIDs []string, alertText string) error {
    start := time.Now()
    defer func() {
        jpushPushLatency.Observe(float64(time.Since(start).Milliseconds()))
    }()

    err := // ... JPush推送逻辑

    if err != nil {
        jpushPushTotal.WithLabelValues("failed").Inc()
        return err
    }

    jpushPushTotal.WithLabelValues("success").Inc()
    return nil
}
```

### 10.3 日志规范

```go
// 推送成功日志
m.Info("JPush推送成功",
    zap.String("uid", toUID),
    zap.Int("device_count", len(registrationIDs)),
    zap.Int64("message_id", messageID),
)

// 推送失败日志
m.Error("JPush推送失败",
    zap.String("uid", toUID),
    zap.Int64("message_id", messageID),
    zap.Error(err),
    zap.String("jpush_msg_id", jpushMsgID),
)
```

---

## 十一、常见问题

### Q1: 如何处理用户多设备推送？

**A**: 一个用户可能有多个设备（如iPhone、iPad），需要：
1. 在 `user_push_tokens` 表中为每个设备存储独立的记录
2. 查询时获取该用户所有有效的 `registration_id`
3. 在JPush请求中同时推送到所有设备

```go
func (m *Manager) getUserPushTokens(uid string) ([]string, error) {
    // 查询该用户所有有效的推送Token
    query := "SELECT registration_id FROM user_push_tokens WHERE uid = ? AND status = 1"
    // 执行查询...
    return registrationIDs, nil
}
```

### Q2: 推送失败如何处理？

**A**:
1. WuKongIM已内置重试机制（`MsgNotifyEventRetryMaxCount`配置项）
2. JPush SDK自带失败重试
3. 建议记录推送失败日志到 `push_logs` 表，定期分析失败原因
4. 对于长期失败的Token，标记为无效状态

### Q3: 如何避免重复推送？

**A**:
1. WuKongIM的 `EventMsgOffline` 只会为真正离线的用户触发
2. 如果同时启用了Webhook和内置推送，需要选择其一或做去重处理
3. 推荐使用 `message_id` 作为推送去重的唯一标识

### Q4: 推送内容如何加密？

**A**:
1. WuKongIM发送的 `payload` 字段已经是Base64编码
2. 如果需要端到端加密，需要在App端解密
3. JPush推送的通知内容（alert）通常是明文，可以只推送"您有新消息"，具体内容在App内显示

### Q5: 如何支持推送点击跳转？

**A**: 在JPush推送中添加 `extras` 字段：

```go
payload.SetExtras(map[string]interface{}{
    "message_id": messageID,
    "channel_id": channelID,
    "channel_type": channelType,
})
```

移动端接收推送后解析 `extras` 进行页面跳转。

---

## 十二、参考资料

### 12.1 WuKongIM相关

- [WuKongIM官方文档](https://githubim.com)
- [WuKongIM架构文档](https://deepwiki.com/WuKongIM/WuKongIM)
- [WuKongIM GitHub](https://github.com/WuKongIM/WuKongIM)
- [Webhook配置说明](https://githubim.com/server/advance/webhook.html)

### 12.2 JPush相关

- [JPush官方文档](https://docs.jiguang.cn/jpush/guideline/intro/)
- [JPush REST API v3](https://docs.jiguang.cn/jpush/server/push/rest_api_v3_push)
- [JPush Go SDK](https://github.com/ylywyn/jpush-api-go-client)
- [JPush iOS SDK集成](https://docs.jiguang.cn/jpush/client/iOS/ios_sdk)
- [JPush Android SDK集成](https://docs.jiguang.cn/jpush/client/Android/android_sdk)

### 12.3 WuKongIM核心源码位置

| 功能模块 | 源码路径 |
|---------|---------|
| 离线用户检测 | `internal/channel/handler/event_distribute.go:195` |
| 离线推送处理 | `internal/pusher/handler/event_pushoffline.go:9` |
| Webhook通知 | `internal/webhook/webhook.go:228` |
| 事件定义 | `internal/eventbus/event.go:98` |
| 配置管理 | `internal/options/options.go:127` |

---

## 十三、总结

### 13.1 推荐方案

**生产环境推荐使用方案A（Webhook集成）**，原因如下：
1. ✅ 不修改WuKongIM核心代码，升级无风险
2. ✅ 松耦合架构，易于维护和扩展
3. ✅ 推送服务独立部署，可独立扩展
4. ✅ 易于切换推送服务商（如从JPush切换到个推）
5. ✅ 便于测试和调试

**小型项目或性能要求极高场景可考虑方案B（内置集成）**。

### 13.2 实施建议

1. **分阶段实施**
   - 第一阶段：搭建中间推送服务，实现基本推送
   - 第二阶段：完善用户Token管理和数据库设计
   - 第三阶段：优化推送性能，添加监控告警
   - 第四阶段：支持多推送服务商（FCM、APNs等）

2. **风险控制**
   - 生产环境先小范围灰度测试
   - 保留Webhook日志，便于问题排查
   - 监控推送成功率和延迟

3. **性能优化**
   - 批量推送（JPush支持单次推送1000个设备）
   - 缓存用户的推送Token
   - 异步处理推送逻辑

### 13.3 后续扩展

1. **支持多推送服务商**
   - FCM（Firebase Cloud Messaging）
   - 华为推送（HMS Push）
   - 小米推送
   - OPPO推送
   - VIVO推送

2. **智能推送策略**
   - 根据用户时区调整推送时间
   - 推送内容个性化
   - 推送频率控制

3. **推送数据分析**
   - 推送送达率统计
   - 推送点击率统计
   - 用户推送偏好分析

---

**文档编写**: Claude Code
**审核状态**: 待审核
**更新日期**: 2025-10-18
