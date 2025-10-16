# WuKongIM 离线推送集成方案

## 文档信息

- **方案名称**：内置离线推送 + 保留Webhook兼容方案
- **创建日期**：2025-10-06
- **方案版本**：v1.0
- **负责人**：待定
- **状态**：方案评估中

---

## 一、方案背景

### 1.1 业务需求

离线推送是IM系统的**核心功能**，用户在不在线时需要通过手机厂商的推送服务（APNs、小米推送、华为推送等）接收消息通知。

### 1.2 现状分析

**当前架构**：
- WuKongIM 通过 Webhook 机制通知离线消息
- 需要用户自己开发独立的推送服务来对接厂商SDK
- 离线判定逻辑：`internal/channel/handler/event_distribute.go:140`

**存在问题**：
1. **部署复杂**：需要额外维护一个 Webhook 推送服务
2. **架构复杂**：增加了系统的整体复杂度
3. **延迟增加**：多一次 HTTP 调用链路
4. **故障点增加**：Webhook 服务可能成为单点故障

### 1.3 方案目标

1. ✅ **降低部署复杂度**：推送功能内置，开箱即用
2. ✅ **提升性能**：减少 HTTP 调用开销
3. ✅ **保持兼容性**：不影响已有用户的 Webhook 使用
4. ✅ **模块化设计**：推送逻辑独立，易于维护
5. ✅ **灵活配置**：支持启用/禁用，支持多厂商

---

## 二、离线判定机制详解

### 2.1 多端登录架构

WuKongIM 支持多端同时登录，设备通过 `device_flag` 和 `device_level` 进行区分：

**设备标识 (device_flag)**：
```
0 - App
1 - Web
2 - PC
3 - iPad
... 可自定义扩展
```

**设备等级 (device_level)**：
```go
// 来源：github.com/WuKongIM/WuKongIMGoProto
const (
    DeviceLevelSlave  = 0  // 从设备（默认）
    DeviceLevelMaster = 1  // 主设备
)
```

### 2.2 离线判定逻辑

**核心代码位置**：`internal/channel/handler/event_distribute.go:399-409`

```go
// 用户的设备在线状态
func (h *Handler) deviceOnlineStatus(uid string) (bool, bool) {
    toConns := eventbus.User.AuthedConnsByUid(uid)  // 获取用户所有在线连接
    masterIsOnline := false
    for _, conn := range toConns {
        if conn.DeviceLevel == wkproto.DeviceLevelMaster {
            masterIsOnline = true
            break
        }
    }
    return len(toConns) > 0, masterIsOnline
}
```

**返回值说明**：
- 第一个返回值 `isOnline`：只要有**任意设备**在线就返回 `true`
- 第二个返回值 `masterIsOnline`：只有**主设备**在线才返回 `true`

### 2.3 离线推送触发条件

**代码位置**：`internal/channel/handler/event_distribute.go:140-146`

```go
isOnline, masterIsOnline := h.deviceOnlineStatus(uid)
if !masterIsOnline && channelType != wkproto.ChannelTypeAgent {
    // 收集离线用户
    offlineUids = append(offlineUids, uid)
}
```

**判定规则**：
```
触发离线推送的条件 = !masterIsOnline（主设备不在线）
```

### 2.4 离线判定场景示例

| 场景 | 在线设备 | Device Level | isOnline | masterIsOnline | 是否推送 | 说明 |
|------|---------|--------------|----------|----------------|---------|------|
| 场景1 | 无 | - | false | false | ✅ 推送 | 用户完全离线 |
| 场景2 | App | Master | true | true | ❌ 不推送 | 主设备在线 |
| 场景3 | Web | Master | true | true | ❌ 不推送 | 主设备在线 |
| 场景4 | iPad | Slave | true | false | ✅ 推送 | 只有从设备在线 |
| 场景5 | App + Web | Master + Slave | true | true | ❌ 不推送 | 有主设备在线 |
| 场景6 | iPad + PC | Slave + Slave | true | false | ✅ 推送 | 都是从设备 |

### 2.5 设备等级设置

**设备等级配置位置**：`internal/api/user.go:335-351`

用户通过设备上线接口设置设备等级：

```json
POST /user/device_update
{
  "uid": "user001",
  "device_flag": 0,
  "device_level": 1,  // 0=Slave, 1=Master
  "device_id": "device_unique_id",
  "token": "push_token"
}
```

**设备等级行为**：
- **Master 设备上线**：会踢掉该用户的所有其他连接（互斥登录）
- **Slave 设备上线**：只踢掉相同 `device_id` 的旧连接（可多端登录）

### 2.6 推送策略建议

**典型配置方案**：

| 设备类型 | device_flag | device_level | 推送Token存储 | 说明 |
|---------|-------------|--------------|--------------|------|
| iOS App | 0 | Master | APNs Token | 主设备，独占 |
| Android App | 0 | Master | FCM/厂商Token | 主设备，独占 |
| Web | 1 | Slave | WebPush Token | 辅助设备 |
| PC | 2 | Slave | 无需推送 | 辅助设备 |
| iPad | 3 | Slave | APNs Token | 辅助设备 |

**推送逻辑**：
1. 用户在 **App (Master)** 上在线 → 不推送（消息已实时送达）
2. 用户只在 **Web/PC (Slave)** 上在线 → **推送到 App**（提醒用户查看手机）
3. 用户完全离线 → **推送到所有注册过Token的设备**

---

## 三、技术方案设计

### 3.1 架构设计

#### 3.1.1 整体架构图

```
┌─────────────────────────────────────────────────────────────┐
│                    WuKongIM 核心系统                         │
│                                                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Pusher Handler (离线推送入口)                        │   │
│  │  internal/pusher/handler/event_pushoffline.go        │   │
│  └────────────┬──────────────┬──────────────────────────┘   │
│               │              │                               │
│               ▼              ▼                               │
│  ┌────────────────┐  ┌──────────────────┐                   │
│  │ Offline Push   │  │ Webhook Service  │                   │
│  │ Manager (新增)  │  │  (保持兼容)       │                   │
│  └────────┬───────┘  └──────────────────┘                   │
│           │                                                  │
│           ├─────┬─────┬─────┬─────┬─────┐                   │
│           ▼     ▼     ▼     ▼     ▼     ▼                   │
│        ┌────┐┌────┐┌────┐┌────┐┌────┐┌────┐                │
│        │APNs││小米││华为││FCM ││OPPO││Vivo│                │
│        └────┘└────┘└────┘└────┘└────┘└────┘                │
└─────────────────────────────────────────────────────────────┘
```

#### 3.1.2 模块划分

```
internal/
├── pusher/
│   └── handler/
│       └── event_pushoffline.go    # 修改：添加内置推送调用
│
├── offlinepush/                    # 新增模块
│   ├── manager.go                  # 推送管理器
│   ├── types.go                    # 类型定义
│   ├── device_store.go             # 设备Token存储
│   ├── providers/                  # 推送提供商
│   │   ├── provider.go             # 推送接口定义
│   │   ├── apns/                   # iOS推送
│   │   │   ├── apns.go
│   │   │   └── apns_test.go
│   │   ├── xiaomi/                 # 小米推送
│   │   │   ├── xiaomi.go
│   │   │   └── xiaomi_test.go
│   │   ├── fcm/                    # Google FCM
│   │   │   ├── fcm.go
│   │   │   └── fcm_test.go
│   │   ├── huawei/                 # 华为推送
│   │   │   ├── huawei.go
│   │   │   └── huawei_test.go
│   │   ├── oppo/                   # OPPO推送
│   │   │   ├── oppo.go
│   │   │   └── oppo_test.go
│   │   └── vivo/                   # Vivo推送
│   │       ├── vivo.go
│   │       └── vivo_test.go
│   └── offlinepush_test.go
│
├── service/
│   └── service.go                  # 修改：注册 OfflinePushManager
│
└── options/
    └── options.go                  # 修改：添加离线推送配置
```

### 3.2 核心代码设计

#### 3.2.1 推送接口定义

**文件**：`internal/offlinepush/providers/provider.go`

```go
package providers

import (
	"context"
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// PushProvider 推送提供商接口
type PushProvider interface {
	// Name 获取提供商名称
	Name() string

	// Push 推送消息
	Push(ctx context.Context, req *PushRequest) error

	// IsEnabled 是否启用
	IsEnabled() bool

	// Close 关闭推送服务
	Close() error
}

// PushRequest 推送请求
type PushRequest struct {
	// 设备Token
	DeviceToken string

	// 用户UID
	Uid string

	// 消息内容
	SendPacket *wkproto.SendPacket

	// 事件信息
	Event *eventbus.Event

	// 扩展字段
	Extras map[string]interface{}
}

// PushResult 推送结果
type PushResult struct {
	Success      bool
	ErrorMessage string
	MessageId    string
}
```

#### 3.2.2 推送管理器

**文件**：`internal/offlinepush/manager.go`

```go
package offlinepush

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/offlinepush/providers"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type Manager struct {
	wklog.Log
	enabled   bool
	providers map[string]providers.PushProvider
	store     DeviceStore
	mu        sync.RWMutex
}

func NewManager() *Manager {
	if !options.G.OfflinePush.Enabled {
		return nil
	}

	m := &Manager{
		Log:       wklog.NewWKLog("OfflinePushManager"),
		enabled:   true,
		providers: make(map[string]providers.PushProvider),
		store:     NewDeviceStore(),
	}

	// 初始化各厂商推送
	m.initProviders()

	return m
}

func (m *Manager) initProviders() {
	// APNs
	if options.G.OfflinePush.APNs.Enabled {
		apnsProvider, err := providers.NewAPNsProvider(&options.G.OfflinePush.APNs)
		if err != nil {
			m.Error("Failed to init APNs provider", zap.Error(err))
		} else {
			m.providers["apns"] = apnsProvider
			m.Info("APNs provider initialized")
		}
	}

	// 小米推送
	if options.G.OfflinePush.Xiaomi.Enabled {
		xiaomiProvider, err := providers.NewXiaomiProvider(&options.G.OfflinePush.Xiaomi)
		if err != nil {
			m.Error("Failed to init Xiaomi provider", zap.Error(err))
		} else {
			m.providers["xiaomi"] = xiaomiProvider
			m.Info("Xiaomi provider initialized")
		}
	}

	// FCM
	if options.G.OfflinePush.FCM.Enabled {
		fcmProvider, err := providers.NewFCMProvider(&options.G.OfflinePush.FCM)
		if err != nil {
			m.Error("Failed to init FCM provider", zap.Error(err))
		} else {
			m.providers["fcm"] = fcmProvider
			m.Info("FCM provider initialized")
		}
	}

	// 华为推送
	if options.G.OfflinePush.Huawei.Enabled {
		huaweiProvider, err := providers.NewHuaweiProvider(&options.G.OfflinePush.Huawei)
		if err != nil {
			m.Error("Failed to init Huawei provider", zap.Error(err))
		} else {
			m.providers["huawei"] = huaweiProvider
			m.Info("Huawei provider initialized")
		}
	}

	// OPPO推送
	if options.G.OfflinePush.OPPO.Enabled {
		oppoProvider, err := providers.NewOPPOProvider(&options.G.OfflinePush.OPPO)
		if err != nil {
			m.Error("Failed to init OPPO provider", zap.Error(err))
		} else {
			m.providers["oppo"] = oppoProvider
			m.Info("OPPO provider initialized")
		}
	}

	// Vivo推送
	if options.G.OfflinePush.Vivo.Enabled {
		vivoProvider, err := providers.NewVivoProvider(&options.G.OfflinePush.Vivo)
		if err != nil {
			m.Error("Failed to init Vivo provider", zap.Error(err))
		} else {
			m.providers["vivo"] = vivoProvider
			m.Info("Vivo provider initialized")
		}
	}
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

	// 处理每个离线用户
	for _, uid := range e.OfflineUsers {
		// 避免发送者收到推送
		if uid == e.Conn.Uid {
			continue
		}

		// 获取用户的设备Token
		devices, err := m.store.GetUserDevices(uid)
		if err != nil {
			m.Error("Failed to get user devices", zap.String("uid", uid), zap.Error(err))
			continue
		}

		if len(devices) == 0 {
			m.Debug("No device token found for user", zap.String("uid", uid))
			continue
		}

		// 推送到每个设备
		for _, device := range devices {
			m.pushToDevice(device, sendPacket, e)
		}
	}
}

func (m *Manager) pushToDevice(device *Device, sendPacket *wkproto.SendPacket, e *eventbus.Event) {
	var providerName string

	// 根据平台和厂商选择推送提供商
	switch device.Platform {
	case PlatformIOS:
		providerName = "apns"
	case PlatformAndroid:
		providerName = m.getAndroidProviderName(device.Manufacturer)
	default:
		m.Warn("Unsupported platform", zap.String("platform", device.Platform))
		return
	}

	provider, ok := m.providers[providerName]
	if !ok || !provider.IsEnabled() {
		m.Debug("Provider not available",
			zap.String("provider", providerName),
			zap.String("uid", device.Uid))
		return
	}

	// 构建推送请求
	req := &providers.PushRequest{
		DeviceToken: device.Token,
		Uid:         device.Uid,
		SendPacket:  sendPacket,
		Event:       e,
		Extras:      make(map[string]interface{}),
	}

	// 执行推送（带超时控制）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := provider.Push(ctx, req)
	if err != nil {
		m.Error("Failed to push message",
			zap.String("provider", providerName),
			zap.String("uid", device.Uid),
			zap.Error(err))
	} else {
		m.Info("Message pushed successfully",
			zap.String("provider", providerName),
			zap.String("uid", device.Uid),
			zap.Int64("messageId", e.MessageId))
	}
}

func (m *Manager) getAndroidProviderName(manufacturer string) string {
	switch manufacturer {
	case "Xiaomi":
		return "xiaomi"
	case "Huawei":
		return "huawei"
	case "OPPO":
		return "oppo"
	case "Vivo":
		return "vivo"
	default:
		return "fcm" // 默认使用FCM
	}
}

// Enabled 是否启用
func (m *Manager) Enabled() bool {
	return m.enabled
}

// RegisterDevice 注册设备Token
func (m *Manager) RegisterDevice(device *Device) error {
	return m.store.SaveDevice(device)
}

// UnregisterDevice 注销设备Token
func (m *Manager) UnregisterDevice(uid, deviceId string) error {
	return m.store.DeleteDevice(uid, deviceId)
}

// Close 关闭推送服务
func (m *Manager) Close() error {
	for name, provider := range m.providers {
		if err := provider.Close(); err != nil {
			m.Error("Failed to close provider", zap.String("provider", name), zap.Error(err))
		}
	}
	return nil
}
```

#### 3.2.3 设备Token存储

**文件**：`internal/offlinepush/device_store.go`

```go
package offlinepush

import (
	"encoding/json"
	"fmt"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

// Platform 平台类型
const (
	PlatformIOS     = "ios"
	PlatformAndroid = "android"
)

// Device 设备信息
type Device struct {
	Uid          string `json:"uid"`           // 用户ID
	DeviceId     string `json:"device_id"`     // 设备唯一标识
	Platform     string `json:"platform"`      // 平台: ios, android
	Manufacturer string `json:"manufacturer"`  // 厂商: Apple, Xiaomi, Huawei, OPPO, Vivo等
	Token        string `json:"token"`         // 推送Token
	DeviceFlag   uint64 `json:"device_flag"`   // 设备标识
	DeviceLevel  uint8  `json:"device_level"`  // 设备等级
	Extras       string `json:"extras"`        // 扩展信息（JSON格式）
}

// DeviceStore 设备Token存储接口
type DeviceStore interface {
	// SaveDevice 保存设备信息
	SaveDevice(device *Device) error

	// GetUserDevices 获取用户的所有设备
	GetUserDevices(uid string) ([]*Device, error)

	// GetDevice 获取指定设备
	GetDevice(uid, deviceId string) (*Device, error)

	// DeleteDevice 删除设备
	DeleteDevice(uid, deviceId string) error

	// UpdateToken 更新设备Token
	UpdateToken(uid, deviceId, token string) error
}

type deviceStore struct {
}

func NewDeviceStore() DeviceStore {
	return &deviceStore{}
}

// 使用 wkdb 的 Device 表扩展存储
// Token字段已经存在，需要添加 Platform 和 Manufacturer 到 Extras 中

func (s *deviceStore) SaveDevice(device *Device) error {
	// 构建扩展信息
	extras := map[string]interface{}{
		"platform":     device.Platform,
		"manufacturer": device.Manufacturer,
	}
	if device.Extras != "" {
		var existingExtras map[string]interface{}
		if err := json.Unmarshal([]byte(device.Extras), &existingExtras); err == nil {
			for k, v := range existingExtras {
				extras[k] = v
			}
		}
	}
	extrasJson, _ := json.Marshal(extras)

	// 保存到 wkdb
	wkDevice := wkdb.Device{
		Uid:         device.Uid,
		DeviceFlag:  device.DeviceFlag,
		DeviceLevel: device.DeviceLevel,
		Token:       device.Token,
		DeviceId:    device.DeviceId,
		Extras:      string(extrasJson),
	}

	return service.Store.AddOrUpdateDevice(&wkDevice)
}

func (s *deviceStore) GetUserDevices(uid string) ([]*Device, error) {
	wkDevices, err := service.Store.GetDevices(uid)
	if err != nil {
		return nil, err
	}

	devices := make([]*Device, 0, len(wkDevices))
	for _, wkDevice := range wkDevices {
		device := s.convertFromWKDevice(&wkDevice)
		// 只返回有Token的设备
		if device.Token != "" {
			devices = append(devices, device)
		}
	}

	return devices, nil
}

func (s *deviceStore) GetDevice(uid, deviceId string) (*Device, error) {
	wkDevice, err := service.Store.GetDevice(uid, deviceId)
	if err != nil {
		return nil, err
	}

	return s.convertFromWKDevice(wkDevice), nil
}

func (s *deviceStore) DeleteDevice(uid, deviceId string) error {
	return service.Store.DeleteDevice(uid, deviceId)
}

func (s *deviceStore) UpdateToken(uid, deviceId, token string) error {
	wkDevice, err := service.Store.GetDevice(uid, deviceId)
	if err != nil {
		return err
	}

	wkDevice.Token = token
	return service.Store.AddOrUpdateDevice(wkDevice)
}

func (s *deviceStore) convertFromWKDevice(wkDevice *wkdb.Device) *Device {
	device := &Device{
		Uid:         wkDevice.Uid,
		DeviceId:    wkDevice.DeviceId,
		Token:       wkDevice.Token,
		DeviceFlag:  wkDevice.DeviceFlag,
		DeviceLevel: wkDevice.DeviceLevel,
		Extras:      wkDevice.Extras,
	}

	// 从 Extras 中解析 Platform 和 Manufacturer
	if wkDevice.Extras != "" {
		var extras map[string]interface{}
		if err := json.Unmarshal([]byte(wkDevice.Extras), &extras); err == nil {
			if platform, ok := extras["platform"].(string); ok {
				device.Platform = platform
			}
			if manufacturer, ok := extras["manufacturer"].(string); ok {
				device.Manufacturer = manufacturer
			}
		}
	}

	return device
}
```

#### 3.2.4 APNs推送实现示例

**文件**：`internal/offlinepush/providers/apns/apns.go`

```go
package apns

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/offlinepush/providers"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/token"
	"go.uber.org/zap"
)

type APNsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	KeyId      string `yaml:"keyId"`      // APNs Key ID
	TeamId     string `yaml:"teamId"`     // Apple Team ID
	BundleId   string `yaml:"bundleId"`   // App Bundle ID
	KeyPath    string `yaml:"keyPath"`    // .p8 key file path
	Production bool   `yaml:"production"` // 是否生产环境
}

type APNsProvider struct {
	wklog.Log
	config *APNsConfig
	client *apns2.Client
}

func NewAPNsProvider(config *APNsConfig) (providers.PushProvider, error) {
	authKey, err := token.AuthKeyFromFile(config.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load APNs auth key: %w", err)
	}

	tokenProvider := &token.Token{
		AuthKey: authKey,
		KeyID:   config.KeyId,
		TeamID:  config.TeamId,
	}

	client := apns2.NewTokenClient(tokenProvider)
	if config.Production {
		client.Production()
	} else {
		client.Development()
	}

	return &APNsProvider{
		Log:    wklog.NewWKLog("APNsProvider"),
		config: config,
		client: client,
	}, nil
}

func (p *APNsProvider) Name() string {
	return "apns"
}

func (p *APNsProvider) IsEnabled() bool {
	return p.config.Enabled
}

func (p *APNsProvider) Push(ctx context.Context, req *providers.PushRequest) error {
	// 解析消息内容
	var payload map[string]interface{}
	if err := json.Unmarshal(req.SendPacket.Payload, &payload); err != nil {
		p.Error("Failed to unmarshal payload", zap.Error(err))
		payload = map[string]interface{}{
			"content": string(req.SendPacket.Payload),
		}
	}

	// 构建APNs通知
	notification := &apns2.Notification{
		DeviceToken: req.DeviceToken,
		Topic:       p.config.BundleId,
		Payload: map[string]interface{}{
			"aps": map[string]interface{}{
				"alert": map[string]interface{}{
					"title": "新消息",
					"body":  p.getMessagePreview(payload),
				},
				"badge": 1,
				"sound": "default",
			},
			"message_id":   req.Event.MessageId,
			"channel_id":   req.SendPacket.ChannelID,
			"channel_type": req.SendPacket.ChannelType,
			"from_uid":     req.Event.Conn.Uid,
		},
	}

	// 发送推送
	res, err := p.client.PushWithContext(ctx, notification)
	if err != nil {
		return fmt.Errorf("APNs push failed: %w", err)
	}

	if res.StatusCode != 200 {
		return fmt.Errorf("APNs returned status %d: %s", res.StatusCode, res.Reason)
	}

	p.Info("APNs push success",
		zap.String("uid", req.Uid),
		zap.String("apnsId", res.ApnsID))

	return nil
}

func (p *APNsProvider) getMessagePreview(payload map[string]interface{}) string {
	if content, ok := payload["content"].(string); ok {
		if len(content) > 50 {
			return content[:50] + "..."
		}
		return content
	}
	return "您有一条新消息"
}

func (p *APNsProvider) Close() error {
	// APNs client 不需要显式关闭
	return nil
}
```

#### 3.2.5 修改离线推送入口

**文件**：`internal/pusher/handler/event_pushoffline.go`

```go
package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
)

func (h *Handler) pushOffline(ctx *eventbus.PushContext) {
	for _, e := range ctx.Events {

		// ========== 1. 原有逻辑：AI推送 ==========
		for _, toUid := range e.OfflineUsers {
			fromUid := e.Conn.Uid
			if fromUid != toUid && h.isAI(toUid) && !e.Frame.GetsyncOnce() && !options.G.IsSystemUid(fromUid) {
				h.processAIPush(toUid, e)
			}
		}

		// ========== 2. 新增：内置厂商推送 ==========
		if service.OfflinePushManager != nil && service.OfflinePushManager.Enabled() {
			service.OfflinePushManager.Push(e)
		}

		// ========== 3. 原有逻辑：Webhook通知（保持兼容）==========
		// 如果启用了内置推送，可以选择不触发Webhook，避免重复推送
		// 这里通过配置控制
		if options.G.WebhookOn(types.EventMsgOffline) {
			// 检查是否禁用Webhook（当内置推送启用时）
			if !options.G.OfflinePush.DisableWebhook {
				service.Webhook.NotifyOfflineMsg(ctx.Events)
			}
		}
	}
}
```

#### 3.2.6 注册推送管理器

**文件**：`internal/service/service.go`

```go
package service

import (
	"github.com/WuKongIM/WuKongIM/internal/offlinepush"
	// ... 其他导入
)

var (
	// ... 其他服务

	// OfflinePushManager 离线推送管理器
	OfflinePushManager *offlinepush.Manager
)

func Init() {
	// ... 其他初始化

	// 初始化离线推送管理器
	OfflinePushManager = offlinepush.NewManager()
	if OfflinePushManager != nil {
		Log.Info("Offline push manager initialized")
	}
}
```

### 3.3 配置设计

#### 3.3.1 配置文件

**文件**：`wukongim.yaml`

```yaml
# ==================== 离线推送配置 ====================
offlinePush:
  enabled: true                    # 是否启用内置离线推送
  disableWebhook: true            # 启用内置推送后是否禁用Webhook（避免重复推送）

  # iOS APNs 推送
  apns:
    enabled: true
    keyId: "YOUR_KEY_ID"          # APNs Key ID
    teamId: "YOUR_TEAM_ID"        # Apple Team ID
    bundleId: "com.yourapp.bundle"  # App Bundle ID
    keyPath: "/path/to/AuthKey_XXXXX.p8"  # .p8 密钥文件路径
    production: false              # 是否生产环境（false=开发环境，true=生产环境）

  # 小米推送
  xiaomi:
    enabled: true
    appId: "YOUR_APP_ID"
    appKey: "YOUR_APP_KEY"
    appSecret: "YOUR_APP_SECRET"
    production: false

  # Google FCM 推送
  fcm:
    enabled: true
    serverKey: "YOUR_SERVER_KEY"
    # 或者使用服务账号JSON文件
    credentialsPath: "/path/to/firebase-credentials.json"

  # 华为推送
  huawei:
    enabled: true
    appId: "YOUR_APP_ID"
    appSecret: "YOUR_APP_SECRET"

  # OPPO推送
  oppo:
    enabled: true
    appKey: "YOUR_APP_KEY"
    masterSecret: "YOUR_MASTER_SECRET"

  # Vivo推送
  vivo:
    enabled: true
    appId: "YOUR_APP_ID"
    appKey: "YOUR_APP_KEY"
    appSecret: "YOUR_APP_SECRET"

# ==================== Webhook配置（保持兼容）====================
webhook:
  httpAddr: "http://your-service.com/webhook"  # 如果启用了内置推送，这个可以不配置
  # ... 其他webhook配置
```

#### 3.3.2 配置结构定义

**文件**：`internal/options/options.go`

```go
// 添加到 Options 结构体
type Options struct {
	// ... 现有字段

	// OfflinePush 离线推送配置
	OfflinePush struct {
		Enabled        bool // 是否启用
		DisableWebhook bool // 是否禁用Webhook

		APNs struct {
			Enabled    bool
			KeyId      string
			TeamId     string
			BundleId   string
			KeyPath    string
			Production bool
		}

		Xiaomi struct {
			Enabled    bool
			AppId      string
			AppKey     string
			AppSecret  string
			Production bool
		}

		FCM struct {
			Enabled         bool
			ServerKey       string
			CredentialsPath string
		}

		Huawei struct {
			Enabled   bool
			AppId     string
			AppSecret string
		}

		OPPO struct {
			Enabled      bool
			AppKey       string
			MasterSecret string
		}

		Vivo struct {
			Enabled   bool
			AppId     string
			AppKey    string
			AppSecret string
		}
	}
}
```

### 3.4 API 设计

#### 3.4.1 设备Token注册接口

**文件**：`internal/api/offlinepush.go`（新建）

```go
package api

import (
	"github.com/WuKongIM/WuKongIM/internal/offlinepush"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"net/http"
)

type offlinePushAPI struct {
	s *Server
}

func newOfflinePushAPI(s *Server) *offlinePushAPI {
	return &offlinePushAPI{s: s}
}

func (a *offlinePushAPI) route(r *wkhttp.WKHttp) {
	r.POST("/push/device/register", a.registerDevice)   // 注册设备Token
	r.POST("/push/device/unregister", a.unregisterDevice) // 注销设备Token
}

// 注册设备Token
func (a *offlinePushAPI) registerDevice(c *wkhttp.Context) {
	var req struct {
		Uid          string `json:"uid" binding:"required"`
		DeviceId     string `json:"device_id" binding:"required"`
		Platform     string `json:"platform" binding:"required"`      // ios, android
		Manufacturer string `json:"manufacturer"`                     // 厂商名称（Android必填）
		Token        string `json:"token" binding:"required"`         // 推送Token
		DeviceFlag   uint64 `json:"device_flag"`                      // 设备标识
		DeviceLevel  uint8  `json:"device_level"`                     // 设备等级
		Extras       string `json:"extras"`                           // 扩展信息
	}

	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(err)
		return
	}

	// 验证平台
	if req.Platform != "ios" && req.Platform != "android" {
		c.ResponseError(errors.New("invalid platform, must be 'ios' or 'android'"))
		return
	}

	// Android必须指定厂商
	if req.Platform == "android" && req.Manufacturer == "" {
		c.ResponseError(errors.New("manufacturer is required for Android"))
		return
	}

	device := &offlinepush.Device{
		Uid:          req.Uid,
		DeviceId:     req.DeviceId,
		Platform:     req.Platform,
		Manufacturer: req.Manufacturer,
		Token:        req.Token,
		DeviceFlag:   req.DeviceFlag,
		DeviceLevel:  req.DeviceLevel,
		Extras:       req.Extras,
	}

	if service.OfflinePushManager == nil {
		c.ResponseError(errors.New("offline push is not enabled"))
		return
	}

	err := service.OfflinePushManager.RegisterDevice(device)
	if err != nil {
		a.Error("Failed to register device", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

// 注销设备Token
func (a *offlinePushAPI) unregisterDevice(c *wkhttp.Context) {
	var req struct {
		Uid      string `json:"uid" binding:"required"`
		DeviceId string `json:"device_id" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(err)
		return
	}

	if service.OfflinePushManager == nil {
		c.ResponseError(errors.New("offline push is not enabled"))
		return
	}

	err := service.OfflinePushManager.UnregisterDevice(req.Uid, req.DeviceId)
	if err != nil {
		a.Error("Failed to unregister device", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}
```

#### 3.4.2 路由注册

**文件**：`internal/api/server.go`

```go
// 在 route 方法中添加
func (s *Server) route(r *wkhttp.WKHttp) {
	// ... 现有路由

	// 离线推送路由
	if options.G.OfflinePush.Enabled {
		newOfflinePushAPI(s).route(r)
	}
}
```

---

## 四、实施计划

### 4.1 阶段划分

#### **Phase 1：基础框架搭建（1周）**

**目标**：完成核心模块框架和APNs推送

**任务**：
1. ✅ 创建 `internal/offlinepush` 模块目录结构
2. ✅ 实现推送接口定义 (`providers/provider.go`)
3. ✅ 实现推送管理器 (`manager.go`)
4. ✅ 实现设备存储 (`device_store.go`)
5. ✅ 实现APNs推送 (`providers/apns/apns.go`)
6. ✅ 添加配置支持
7. ✅ 修改 `event_pushoffline.go` 集成内置推送
8. ✅ 添加API接口（设备注册/注销）
9. ✅ 编写单元测试

**交付物**：
- APNs推送可用
- 完整的测试用例
- API文档

#### **Phase 2：Android厂商推送（2周）**

**目标**：完成所有Android厂商推送集成

**任务**：
1. ✅ 实现小米推送 (`providers/xiaomi/xiaomi.go`)
2. ✅ 实现华为推送 (`providers/huawei/huawei.go`)
3. ✅ 实现OPPO推送 (`providers/oppo/oppo.go`)
4. ✅ 实现Vivo推送 (`providers/vivo/vivo.go`)
5. ✅ 实现FCM推送 (`providers/fcm/fcm.go`)
6. ✅ 厂商推送单元测试
7. ✅ 集成测试

**交付物**：
- 所有Android厂商推送可用
- 完整的测试覆盖
- 推送统计和监控

#### **Phase 3：优化与文档（1周）**

**目标**：性能优化、监控、文档完善

**任务**：
1. ✅ 推送失败重试机制
2. ✅ 推送统计和监控指标
3. ✅ 性能优化（批量推送、连接池等）
4. ✅ 编写用户文档
5. ✅ 编写运维文档
6. ✅ 示例代码和最佳实践

**交付物**：
- 完整的用户文档
- 运维手册
- 示例代码

#### **Phase 4：测试与发布（1周）**

**目标**：全面测试，准备发布

**任务**：
1. ✅ 功能测试
2. ✅ 性能测试
3. ✅ 压力测试
4. ✅ 兼容性测试
5. ✅ 代码审查
6. ✅ 准备发布说明

**交付物**：
- 测试报告
- 发布版本
- Release Notes

### 4.2 资源需求

**人力**：
- 后端开发工程师：2人
- 测试工程师：1人
- 技术文档工程师：0.5人

**外部依赖**：
- APNs SDK：`github.com/sideshow/apns2`
- 小米推送SDK：`github.com/NicholeGit/go-mipush`
- 华为推送SDK：需要调用HTTP API
- FCM SDK：`firebase.google.com/go/v4`
- OPPO/Vivo：需要调用HTTP API

### 4.3 风险评估

| 风险项 | 影响 | 概率 | 缓解措施 |
|--------|------|------|---------|
| 厂商SDK不稳定 | 高 | 中 | 增加重试机制，提供降级方案 |
| 推送失败率高 | 中 | 中 | 完善监控，快速定位问题 |
| 性能影响 | 中 | 低 | 异步推送，连接池优化 |
| 配置复杂 | 低 | 中 | 详细文档，合理默认值 |

---

## 五、技术细节

### 5.1 推送流程图

```
用户A发送消息给用户B
         │
         ▼
  检查B的在线状态
         │
    ┌────┴────┐
    │         │
   在线      离线
    │         │
    │         ▼
    │   获取B的Master设备状态
    │         │
    │    ┌────┴────┐
    │    │         │
    │  Master    Master
    │  在线      不在线
    │    │         │
    │   不推       ▼
    │         收集离线用户
    │              │
    │              ▼
    │      触发离线推送事件
    │              │
    │         ┌────┴────┐
    │         │         │
    │    内置推送    Webhook
    │         │         │
    │         ▼         ▼
    │   OfflinePush  外部服务
    │    Manager
    │         │
    │         ▼
    │   获取设备Token
    │         │
    │    ┌────┴────┬────────┬─────┐
    │    │         │        │     │
    │   iOS    Android  Android  ...
    │  APNs    Xiaomi   Huawei
    │    │         │        │
    │    └─────────┴────────┘
    │              │
    └──────────────▼
            推送成功/失败
```

### 5.2 设备Token管理

**Token存储结构**：

```sql
-- 扩展现有的 device 表
Table: device
Columns:
  - id (PRIMARY KEY)
  - uid (用户ID)
  - device_id (设备唯一标识)
  - device_flag (设备标识：0=App, 1=Web, 2=PC, ...)
  - device_level (设备等级：0=Slave, 1=Master)
  - token (推送Token) -- 已存在
  - extras (扩展字段，JSON格式) -- 存储 platform, manufacturer
  - created_at
  - updated_at
```

**Extras JSON 格式**：
```json
{
  "platform": "ios",           // ios, android
  "manufacturer": "Apple",     // Apple, Xiaomi, Huawei, OPPO, Vivo
  "app_version": "1.0.0",
  "os_version": "iOS 15.0"
}
```

### 5.3 推送消息格式

**APNs格式**：
```json
{
  "aps": {
    "alert": {
      "title": "新消息",
      "body": "消息内容预览..."
    },
    "badge": 1,
    "sound": "default"
  },
  "message_id": 123456789,
  "channel_id": "user002",
  "channel_type": 1,
  "from_uid": "user001"
}
```

**Android厂商格式**（以小米为例）：
```json
{
  "title": "新消息",
  "description": "消息内容预览...",
  "payload": {
    "message_id": "123456789",
    "channel_id": "user002",
    "channel_type": 1,
    "from_uid": "user001"
  },
  "pass_through": 0,
  "notify_type": 1
}
```

### 5.4 推送策略

**推送优先级**：
1. **高优先级**：单聊消息、@消息
2. **普通优先级**：群聊消息
3. **低优先级**：系统通知

**推送限流**：
- 单用户推送频率限制：10次/分钟
- 全局推送限制：10000次/秒
- 失败重试：最多3次，指数退避

**推送统计**：
- 推送成功率
- 推送耗时
- 厂商推送分布
- 失败原因统计

---

## 六、测试计划

### 6.1 单元测试

**测试覆盖**：
- 推送管理器
- 各厂商推送提供商
- 设备Token存储
- 配置加载

**测试用例**：
- 正常推送流程
- 推送失败重试
- Token过期处理
- 并发推送

### 6.2 集成测试

**测试场景**：
- 用户注册设备Token
- 用户离线，触发推送
- 多设备推送
- 推送失败降级

### 6.3 性能测试

**测试指标**：
- 推送吞吐量：目标 10000 次/秒
- 推送延迟：P99 < 500ms
- 内存占用：< 100MB（空闲状态）
- CPU占用：< 10%（空闲状态）

---

## 七、运维方案

### 7.1 监控指标

**系统指标**：
- 推送成功率
- 推送失败率
- 推送耗时（P50, P95, P99）
- Token有效率

**业务指标**：
- 各厂商推送量分布
- 离线用户推送量
- 推送到达率

### 7.2 告警规则

**告警级别**：
- **P0（紧急）**：推送成功率 < 90%
- **P1（严重）**：推送成功率 < 95%
- **P2（警告）**：推送延迟 P99 > 1s

### 7.3 故障处理

**常见问题**：
1. **Token失效**：自动清理，通知客户端重新注册
2. **厂商限流**：启用限流保护，降级到FCM
3. **网络故障**：重试机制，失败降级到Webhook

---

## 八、兼容性说明

### 8.1 向后兼容

**现有Webhook用户**：
- 默认不启用内置推送（`offlinePush.enabled: false`）
- Webhook机制继续正常工作
- 可选择性启用内置推送

**升级路径**：
1. 升级WuKongIM到新版本
2. 配置内置推送参数
3. 设置 `offlinePush.enabled: true`
4. 客户端调用API注册设备Token
5. 验证推送正常后，可选择禁用Webhook（`offlinePush.disableWebhook: true`）

### 8.2 数据迁移

**如果已有Webhook推送服务**：

可以通过API批量导入已有的设备Token：

```bash
POST /push/device/register
{
  "uid": "user001",
  "device_id": "device_unique_id",
  "platform": "ios",
  "token": "existing_apns_token",
  "device_flag": 0,
  "device_level": 1
}
```

---

## 九、文档计划

### 9.1 用户文档

**内容**：
- 快速开始指南
- 配置说明
- API接口文档
- 常见问题FAQ
- 最佳实践

### 9.2 开发文档

**内容**：
- 架构设计文档（本文档）
- 代码结构说明
- 接口设计文档
- 扩展开发指南（新增推送厂商）

### 9.3 运维文档

**内容**：
- 部署指南
- 监控配置
- 故障排查
- 性能优化

---

## 十、后续规划

### 10.1 短期规划（3个月）

1. ✅ 完成所有主流厂商推送集成
2. ✅ 推送统计和监控完善
3. ✅ 推送模板功能（自定义推送内容）

### 10.2 中期规划（6个月）

1. ✅ Web Push 支持（PWA）
2. ✅ 推送A/B测试
3. ✅ 推送效果分析（打开率、点击率）

### 10.3 长期规划（1年）

1. ✅ 智能推送（基于用户行为的推送时机优化）
2. ✅ 推送内容个性化
3. ✅ 多语言推送支持

---

## 十一、总结

本方案采用**内置离线推送 + 保留Webhook兼容**的设计：

**核心优势**：
1. ✅ **降低架构复杂度**：推送功能内置，无需额外服务
2. ✅ **开箱即用**：配置简单，快速上手
3. ✅ **保持兼容性**：不影响现有Webhook用户
4. ✅ **模块化设计**：易于扩展新的推送厂商
5. ✅ **性能优异**：减少网络调用，提升推送速度

**适用场景**：
- ✅ 新项目快速启动
- ✅ 简化运维，降低成本
- ✅ 追求极致性能
- ✅ 需要统一推送管理

**不适用场景**：
- ❌ 已有复杂的Webhook推送服务
- ❌ 需要特殊的推送逻辑定制
- ❌ 推送服务需要独立扩展

---

## 附录

### A. 相关代码位置索引

| 功能 | 文件路径 | 说明 |
|------|---------|------|
| 离线判定 | `internal/channel/handler/event_distribute.go:140` | 判断用户是否离线 |
| 设备在线状态 | `internal/channel/handler/event_distribute.go:399` | 获取用户设备在线状态 |
| 离线推送入口 | `internal/pusher/handler/event_pushoffline.go:9` | 离线推送事件处理 |
| Webhook通知 | `internal/webhook/webhook.go:228` | Webhook离线消息通知 |
| 设备管理 | `internal/api/user.go:335` | 设备上线接口 |

### B. 依赖SDK列表

| 推送服务 | SDK | License | 说明 |
|---------|-----|---------|------|
| APNs | github.com/sideshow/apns2 | MIT | 官方推荐 |
| 小米 | github.com/NicholeGit/go-mipush | MIT | 社区维护 |
| FCM | firebase.google.com/go/v4 | Apache 2.0 | Google官方 |
| 华为 | HTTP API | - | 官方HTTP API |
| OPPO | HTTP API | - | 官方HTTP API |
| Vivo | HTTP API | - | 官方HTTP API |

### C. 配置示例

完整配置示例见：[wukongim.yaml.example](./wukongim.yaml.example)

---

**文档维护**：
- 本文档需要随着方案实施进度持续更新
- 重大变更需要版本控制
- 每次评审后更新评审意见

**评审记录**：
| 日期 | 评审人 | 评审意见 | 状态 |
|------|--------|---------|------|
| 2025-10-06 | - | 初始版本 | 待评审 |
