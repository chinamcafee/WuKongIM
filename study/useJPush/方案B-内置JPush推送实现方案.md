# WuKongIM 内置JPush推送实现方案（方案B）

## 文档元信息

- **方案名称**: 方案B - 内置集成方案
- **版本**: v1.0
- **创建日期**: 2025-10-18
- **适用系统**: WuKongIM v2.x
- **参考项目**: OpenIM Server v3.x

---

## 一、方案概述

### 1.1 设计思路

本方案参考 OpenIM Server 的内置推送架构，在 WuKongIM 内部直接集成 JPush SDK，实现移动端离线消息的自动推送。

**核心理念**:
- **借鉴成熟方案**: 复用 OpenIM Server 的设计模式和代码结构
- **模块化设计**: 推送逻辑独立封装，易于扩展和维护
- **多推送服务支持**: 设计统一接口，支持JPush、FCM、个推等多种推送服务
- **可配置化**: 通过配置文件灵活选择推送服务

### 1.2 架构对比

#### OpenIM Server 推送架构

```
消息服务 → Kafka → 推送服务
                      ↓
                  推送器选择器
                      ↓
        ┌─────────────┼─────────────┐
        ↓             ↓             ↓
    JPush推送    FCM推送    个推推送
```

#### WuKongIM 推送架构（方案B）

```
消息发送 → 频道分发 → 在线/离线判断
                          ↓
                    离线推送处理器
                          ↓
                    推送管理器选择器
                          ↓
            ┌─────────────┼─────────────┐
            ↓             ↓             ↓
        JPush推送    FCM推送    Dummy推送
```

### 1.3 技术优势

| 优势项 | 说明 |
|--------|------|
| **性能优势** | 无需额外的HTTP请求，直接调用JPush SDK，推送延迟更低 |
| **简化部署** | 无需部署独立的推送中间服务，降低运维复杂度 |
| **统一管理** | 推送配置、日志、监控与WuKongIM集成在一起 |
| **易于扩展** | 基于接口设计，轻松添加新的推送服务商 |
| **参考实现** | 复用OpenIM成熟的设计，降低开发风险 |

---

## 二、参考OpenIM Server架构分析

### 2.1 OpenIM核心设计要点

经过详细的源码调研（详见 `/Users/changzechuan/IMProjects/open-im-server/USE_JPush.md`），OpenIM的推送架构具有以下特点：

#### 1. 推送器接口设计

```go
// OpenIM的推送器接口定义
type OfflinePusher interface {
    Push(ctx context.Context, userIDs []string, title, content string, opts *options.Opts) error
}
```

**设计优点**:
- 统一的推送接口，所有推送服务实现相同的接口
- 便于切换推送服务商（通过配置文件选择）
- 支持Mock实现，便于测试

#### 2. 推送器工厂模式

```go
// OpenIM的推送器创建逻辑
func NewOfflinePusher(pushConf *config.Push, cache cache.ThirdCache, fcmConfigPath string) (OfflinePusher, error) {
    pushConf.Enable = strings.ToLower(pushConf.Enable)
    switch pushConf.Enable {
    case "getui":
        return getui.NewClient(pushConf, cache), nil
    case "fcm":
        return fcm.NewClient(pushConf, cache, fcmConfigPath)
    case "jpush":
        return jpush.NewClient(pushConf), nil
    default:
        return dummy.NewClient(), nil  // 默认空实现
    }
}
```

**设计优点**:
- 工厂模式创建推送器实例
- 通过配置文件的 `enable` 字段动态选择推送服务
- 提供默认的空实现（Dummy），避免空指针错误

#### 3. JPush模块化结构

OpenIM 将 JPush 相关代码组织为独立的包：

```
internal/push/offlinepush/jpush/
├── push.go              # JPush客户端主逻辑
└── body/                # JPush请求体封装
    ├── platform.go      # 平台选择（iOS/Android等）
    ├── audience.go      # 推送目标（用户别名）
    ├── notification.go  # 通知消息体
    ├── message.go       # 自定义消息体
    ├── pushobj.go       # 推送对象组装
    └── options.go       # 推送选项
```

**设计优点**:
- 清晰的模块划分，每个文件职责单一
- body包封装JPush API的数据结构，与业务逻辑分离
- 便于单元测试和代码维护

#### 4. 配置管理

OpenIM 在统一的配置结构中定义推送相关配置：

```go
type Push struct {
    Enable   string  `yaml:"enable"`    // 启用哪个推送服务: jpush, fcm, getui
    JPush struct {
        AppKey       string `yaml:"appKey"`
        MasterSecret string `yaml:"masterSecret"`
        PushURL      string `yaml:"pushURL"`
        PushIntent   string `yaml:"pushIntent"`
    } `yaml:"jpush"`
    IOSPush struct {
        PushSound    string `yaml:"pushSound"`
        BadgeCount   bool   `yaml:"badgeCount"`
        Production   bool   `yaml:"production"`
    } `yaml:"iosPush"`
}
```

**设计优点**:
- 所有推送服务的配置集中管理
- 支持iOS和Android平台的差异化配置
- 配置结构清晰，易于理解和修改

---

## 三、WuKongIM实现方案

### 3.1 整体设计

基于 OpenIM 的成功经验，我们为 WuKongIM 设计如下实现方案：

```
WuKongIM 离线推送处理流程：

1. 消息发送 → 频道分发器
   ↓
2. 判断用户在线状态，筛选离线用户
   ↓
3. 生成 EventPushOffline 事件
   ↓
4. 离线推送处理器 (internal/pusher/handler/event_pushoffline.go)
   ↓
5. 调用推送管理器 (internal/pusher/offlinepush/manager.go)
   ↓
6. 根据配置选择推送器 (JPush/FCM/Dummy)
   ↓
7. JPush推送器执行推送
   ↓
8. 返回推送结果
```

### 3.2 目录结构设计

```
internal/pusher/
├── handler/
│   ├── base.go                    # 现有：推送处理器基础
│   ├── event_pushonline.go        # 现有：在线推送处理
│   └── event_pushoffline.go       # 修改：添加离线推送调用
└── offlinepush/                   # 新增：离线推送模块
    ├── manager.go                 # 新增：推送管理器（选择推送服务）
    ├── interface.go               # 新增：推送器统一接口
    ├── jpush/                     # 新增：JPush推送实现
    │   ├── push.go                # JPush客户端核心
    │   └── body/                  # JPush请求体封装
    │       ├── platform.go        # 平台选择
    │       ├── audience.go        # 推送目标
    │       ├── notification.go    # 通知消息
    │       ├── message.go         # 自定义消息
    │       ├── pushobj.go         # 推送对象
    │       └── options.go         # 推送选项
    ├── dummy/                     # 新增：空实现（默认）
    │   └── push.go                # Dummy推送器
    └── options/                   # 新增：推送选项
        └── opts.go                # 推送参数封装

internal/options/
└── options.go                     # 修改：添加推送配置

internal/service/
└── common.go                      # 修改：注册推送管理器
```

---

## 四、详细实现代码

### 4.1 配置结构定义

**文件**: `internal/options/options.go`

**位置**: 在 Options 结构体中添加（约第200行后）

```go
// OfflinePush 离线推送配置
OfflinePush struct {
    Enable       string `yaml:"enable"`       // 启用的推送服务: jpush, fcm, dummy
    JPush        struct {
        AppKey       string `yaml:"appKey"`       // JPush AppKey
        MasterSecret string `yaml:"masterSecret"` // JPush MasterSecret
        PushURL      string `yaml:"pushURL"`      // JPush API地址
        PushIntent   string `yaml:"pushIntent"`   // Android点击Intent
    } `yaml:"jpush"`
    FCM struct {
        ProjectID      string `yaml:"projectID"`      // FCM项目ID
        CredentialFile string `yaml:"credentialFile"` // FCM凭证文件路径
    } `yaml:"fcm"`
    IOSPush struct {
        PushSound  string `yaml:"pushSound"`  // iOS推送声音
        BadgeCount bool   `yaml:"badgeCount"` // 是否显示角标
        Production bool   `yaml:"production"` // 是否生产环境
    } `yaml:"iosPush"`
} `yaml:"offlinePush"`
```

**配置文件示例** (`wk.yaml` 或 `exampleconfig/cluster1.yaml`):

```yaml
offlinePush:
  enable: "jpush"                   # 启用的推送服务: jpush, fcm, dummy
  jpush:
    appKey: "your_jpush_appkey"
    masterSecret: "your_master_secret"
    pushURL: "https://api.jpush.cn/v3/push"
    pushIntent: "#Intent;component=com.your.package/.MainActivity;end"
  iosPush:
    pushSound: "default"
    badgeCount: true
    production: false
```

---

### 4.2 推送器接口定义

**新建文件**: `internal/pusher/offlinepush/interface.go`

```go
package offlinepush

import (
	"context"
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
)

// OfflinePusher 离线推送器接口
type OfflinePusher interface {
	// Push 推送离线消息
	// ctx: 上下文
	// userIDs: 目标用户ID列表（用户别名）
	// title: 推送标题
	// content: 推送内容
	// opts: 推送选项（扩展参数）
	Push(ctx context.Context, userIDs []string, title, content string, opts *PushOptions) error
}

// PushOptions 推送选项
type PushOptions struct {
	Ex            string // 自定义扩展数据
	MessageID     int64  // 消息ID
	ClientMsgNo   string // 客户端消息编号
	ChannelID     string // 频道ID
	ChannelType   uint8  // 频道类型
	FromUID       string // 发送者UID
	IOSPushSound  string // iOS推送声音
	IOSBadgeCount bool   // iOS是否显示角标
}

// ConvertFromEvent 从事件转换为推送选项
func ConvertFromEvent(e *eventbus.Event) *PushOptions {
	opts := &PushOptions{
		MessageID:     e.MessageId,
		ClientMsgNo:   e.Frame.(*wkproto.SendPacket).ClientMsgNo,
		ChannelID:     e.ChannelId,
		ChannelType:   e.ChannelType,
		FromUID:       e.Conn.Uid,
		IOSPushSound:  options.G.OfflinePush.IOSPush.PushSound,
		IOSBadgeCount: options.G.OfflinePush.IOSPush.BadgeCount,
	}

	// 构造扩展数据
	extData := map[string]interface{}{
		"channel_id":   e.ChannelId,
		"channel_type": e.ChannelType,
		"message_id":   e.MessageId,
		"from_uid":     e.Conn.Uid,
	}
	opts.Ex = wkutil.ToJSON(extData)

	return opts
}
```

---

### 4.3 推送管理器实现

**新建文件**: `internal/pusher/offlinepush/manager.go`

```go
package offlinepush

import (
	"context"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/pusher/offlinepush/dummy"
	"github.com/WuKongIM/WuKongIM/internal/pusher/offlinepush/jpush"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

const (
	JPushService = "jpush"  // 极光推送
	FCMService   = "fcm"    // Firebase Cloud Messaging
	DummyService = "dummy"  // 空实现（默认）
)

type Manager struct {
	wklog.Log
	pusher OfflinePusher
}

// NewManager 创建推送管理器
func NewManager() *Manager {
	m := &Manager{
		Log: wklog.NewWKLog("OfflinePushManager"),
	}

	// 根据配置创建推送器
	m.pusher = m.createPusher()

	return m
}

// createPusher 创建推送器实例
func (m *Manager) createPusher() OfflinePusher {
	enableService := strings.ToLower(options.G.OfflinePush.Enable)

	m.Info("创建离线推送器", zap.String("service", enableService))

	switch enableService {
	case JPushService:
		if options.G.OfflinePush.JPush.AppKey == "" ||
		   options.G.OfflinePush.JPush.MasterSecret == "" {
			m.Warn("JPush配置不完整，使用Dummy推送器")
			return dummy.NewClient()
		}
		m.Info("使用JPush推送服务")
		return jpush.NewClient()

	case FCMService:
		// TODO: 实现FCM推送
		m.Warn("FCM推送暂未实现，使用Dummy推送器")
		return dummy.NewClient()

	case DummyService:
		fallthrough
	default:
		m.Info("使用Dummy推送器（空实现）")
		return dummy.NewClient()
	}
}

// Enabled 是否启用了离线推送
func (m *Manager) Enabled() bool {
	return options.G.OfflinePush.Enable != "" &&
	       options.G.OfflinePush.Enable != DummyService
}

// Push 推送离线消息
func (m *Manager) Push(ctx context.Context, userIDs []string, title, content string, opts *PushOptions) error {
	return m.pusher.Push(ctx, userIDs, title, content, opts)
}
```

---

### 4.4 JPush推送器实现

#### 4.4.1 核心推送逻辑

**新建文件**: `internal/pusher/offlinepush/jpush/push.go`

```go
package jpush

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/pusher/offlinepush"
	"github.com/WuKongIM/WuKongIM/internal/pusher/offlinepush/jpush/body"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type JPush struct {
	wklog.Log
	httpClient *wkhttp.Client
}

// NewClient 创建JPush客户端
func NewClient() *JPush {
	return &JPush{
		Log:        wklog.NewWKLog("JPush"),
		httpClient: wkhttp.NewClient(),
	}
}

// Push 推送离线消息
func (j *JPush) Push(ctx context.Context, userIDs []string, title, content string, opts *offlinepush.PushOptions) error {
	if len(userIDs) == 0 {
		j.Debug("推送目标用户列表为空，跳过推送")
		return nil
	}

	j.Info("开始JPush推送",
		zap.Int("user_count", len(userIDs)),
		zap.String("title", title),
		zap.Int64("message_id", opts.MessageID),
	)

	// 1. 设置推送平台
	var pf body.Platform
	pf.SetAll()

	// 2. 设置推送目标（使用用户ID作为别名）
	var au body.Audience
	au.SetAlias(userIDs)

	// 3. 构建通知消息
	var no body.Notification
	extras := make(map[string]string)
	extras["ex"] = opts.Ex
	if opts.ClientMsgNo != "" {
		extras["ClientMsgNo"] = opts.ClientMsgNo
	}
	if opts.MessageID > 0 {
		extras["MessageID"] = fmt.Sprintf("%d", opts.MessageID)
	}

	// 设置iOS和Android特定参数
	no.IOSEnableMutableContent()
	no.SetExtras(extras)
	no.SetAlert(content, title, opts)
	no.SetAndroidIntent()

	// 4. 构建自定义消息（透传消息）
	var msg body.Message
	msg.SetMsgContent(content)
	msg.SetTitle(title)
	msg.SetExtras("ex", opts.Ex)
	if opts.ClientMsgNo != "" {
		msg.SetExtras("ClientMsgNo", opts.ClientMsgNo)
	}

	// 5. 设置推送选项
	var opt body.Options
	opt.SetApnsProduction(options.G.OfflinePush.IOSPush.Production)

	// 6. 组装推送对象
	var pushObj body.PushObj
	pushObj.SetPlatform(&pf)
	pushObj.SetAudience(&au)
	pushObj.SetNotification(&no)
	pushObj.SetMessage(&msg)
	pushObj.SetOptions(&opt)

	// 7. 发送推送请求
	var resp map[string]interface{}
	err := j.request(ctx, pushObj, &resp, 5)
	if err != nil {
		j.Error("JPush推送失败",
			zap.Error(err),
			zap.Int("user_count", len(userIDs)),
		)
		return err
	}

	j.Info("JPush推送成功",
		zap.Int("user_count", len(userIDs)),
		zap.Any("response", resp),
	)

	return nil
}

// request 发送HTTP请求到JPush API
func (j *JPush) request(ctx context.Context, pushObj body.PushObj, resp *map[string]interface{}, timeout int) error {
	// 构造请求头
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": j.getAuthorization(),
	}

	// 序列化请求体
	bodyData, err := json.Marshal(pushObj)
	if err != nil {
		return fmt.Errorf("序列化推送对象失败: %w", err)
	}

	j.Debug("JPush请求",
		zap.String("url", options.G.OfflinePush.JPush.PushURL),
		zap.String("body", string(bodyData)),
	)

	// 发送POST请求
	respData, err := j.httpClient.PostJSON(
		ctx,
		options.G.OfflinePush.JPush.PushURL,
		headers,
		bodyData,
		timeout,
	)
	if err != nil {
		return fmt.Errorf("JPush API请求失败: %w", err)
	}

	// 解析响应
	if err := json.Unmarshal(respData, resp); err != nil {
		return fmt.Errorf("解析JPush响应失败: %w", err)
	}

	// 检查是否有错误
	if errObj, ok := (*resp)["error"]; ok {
		return fmt.Errorf("JPush返回错误: %v", errObj)
	}

	// 检查是否有msg_id（推送成功的标识）
	if _, ok := (*resp)["msg_id"]; !ok {
		return fmt.Errorf("JPush响应缺少msg_id: %v", resp)
	}

	return nil
}

// getAuthorization 生成Basic认证头
func (j *JPush) getAuthorization() string {
	appKey := options.G.OfflinePush.JPush.AppKey
	masterSecret := options.G.OfflinePush.JPush.MasterSecret

	str := fmt.Sprintf("%s:%s", appKey, masterSecret)
	encoded := base64.StdEncoding.EncodeToString([]byte(str))

	return fmt.Sprintf("Basic %s", encoded)
}
```

#### 4.4.2 JPush Body结构（参考OpenIM）

**新建文件**: `internal/pusher/offlinepush/jpush/body/platform.go`

```go
package body

const (
	ANDROID      = "android"
	IOS          = "ios"
	QUICKAPP     = "quickapp"
	WINDOWSPHONE = "winphone"
	ALL          = "all"
)

type Platform struct {
	Os     interface{}
	osArry []string
}

func (p *Platform) SetAll() {
	p.Os = ALL
}

func (p *Platform) SetAndroid() error {
	return p.Set(ANDROID)
}

func (p *Platform) SetIOS() error {
	return p.Set(IOS)
}

func (p *Platform) Set(os string) error {
	if p.Os == nil {
		p.osArry = make([]string, 0, 4)
	} else {
		switch p.Os.(type) {
		case string:
			return fmt.Errorf("platform is already set to all")
		}
	}

	// 检查是否已存在
	for _, value := range p.osArry {
		if os == value {
			return nil
		}
	}

	// 添加平台
	switch os {
	case IOS, ANDROID, QUICKAPP, WINDOWSPHONE:
		p.osArry = append(p.osArry, os)
		p.Os = p.osArry
	default:
		return fmt.Errorf("unknown platform: %s", os)
	}

	return nil
}
```

**新建文件**: `internal/pusher/offlinepush/jpush/body/audience.go`

```go
package body

type Audience struct {
	Tag         interface{} `json:"tag,omitempty"`
	TagAnd      interface{} `json:"tag_and,omitempty"`
	TagNot      interface{} `json:"tag_not,omitempty"`
	Alias       interface{} `json:"alias,omitempty"`
	RegistryID  interface{} `json:"registration_id,omitempty"`
	Segment     interface{} `json:"segment,omitempty"`
	Abtest      interface{} `json:"abtest,omitempty"`
}

// SetAlias 设置用户别名（使用用户ID）
func (a *Audience) SetAlias(aliases []string) {
	a.Alias = aliases
}

// SetRegistrationID 设置设备注册ID
func (a *Audience) SetRegistrationID(ids []string) {
	a.RegistryID = ids
}
```

**新建文件**: `internal/pusher/offlinepush/jpush/body/notification.go`

```go
package body

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/pusher/offlinepush"
)

type Notification struct {
	Alert   string  `json:"alert,omitempty"`
	Android Android `json:"android,omitempty"`
	IOS     Ios     `json:"ios,omitempty"`
}

type Android struct {
	Alert  string `json:"alert,omitempty"`
	Title  string `json:"title,omitempty"`
	Intent struct {
		URL string `json:"url,omitempty"`
	} `json:"intent,omitempty"`
	Extras map[string]string `json:"extras,omitempty"`
}

type Ios struct {
	Alert          IosAlert          `json:"alert,omitempty"`
	Sound          string            `json:"sound,omitempty"`
	Badge          string            `json:"badge,omitempty"`
	Extras         map[string]string `json:"extras,omitempty"`
	MutableContent bool              `json:"mutable-content"`
}

type IosAlert struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

// SetAlert 设置通知内容
func (n *Notification) SetAlert(alert string, title string, opts *offlinepush.PushOptions) {
	n.Alert = alert
	n.Android.Alert = alert
	n.Android.Title = title
	n.IOS.Alert.Body = alert
	n.IOS.Alert.Title = title
	n.IOS.Sound = opts.IOSPushSound

	if opts.IOSBadgeCount {
		n.IOS.Badge = "+1"
	}
}

// SetExtras 设置扩展字段
func (n *Notification) SetExtras(extras map[string]string) {
	n.IOS.Extras = extras
	n.Android.Extras = extras
}

// SetAndroidIntent 设置Android点击意图
func (n *Notification) SetAndroidIntent() {
	n.Android.Intent.URL = options.G.OfflinePush.JPush.PushIntent
}

// IOSEnableMutableContent 启用iOS可变内容
func (n *Notification) IOSEnableMutableContent() {
	n.IOS.MutableContent = true
}
```

**新建文件**: `internal/pusher/offlinepush/jpush/body/message.go`

```go
package body

type Message struct {
	MsgContent  string         `json:"msg_content"`
	Title       string         `json:"title,omitempty"`
	ContentType string         `json:"content_type,omitempty"`
	Extras      map[string]any `json:"extras,omitempty"`
}

func (m *Message) SetMsgContent(c string) {
	m.MsgContent = c
}

func (m *Message) SetTitle(t string) {
	m.Title = t
}

func (m *Message) SetExtras(key string, value interface{}) {
	if m.Extras == nil {
		m.Extras = make(map[string]any)
	}
	m.Extras[key] = value
}
```

**新建文件**: `internal/pusher/offlinepush/jpush/body/options.go`

```go
package body

type Options struct {
	SendNo          int  `json:"sendno,omitempty"`
	TimeLive        int  `json:"time_to_live,omitempty"`
	OverrideMsgID   int  `json:"override_msg_id,omitempty"`
	ApnsProduction  bool `json:"apns_production"`
	ApnsCollapseID  string `json:"apns_collapse_id,omitempty"`
	BigPushDuration int  `json:"big_push_duration,omitempty"`
}

func (o *Options) SetApnsProduction(production bool) {
	o.ApnsProduction = production
}
```

**新建文件**: `internal/pusher/offlinepush/jpush/body/pushobj.go`

```go
package body

type PushObj struct {
	Platform     *Platform     `json:"platform"`
	Audience     *Audience     `json:"audience"`
	Notification *Notification `json:"notification,omitempty"`
	Message      *Message      `json:"message,omitempty"`
	Options      *Options      `json:"options,omitempty"`
}

func (p *PushObj) SetPlatform(pf *Platform) {
	p.Platform = pf
}

func (p *PushObj) SetAudience(au *Audience) {
	p.Audience = au
}

func (p *PushObj) SetNotification(no *Notification) {
	p.Notification = no
}

func (p *PushObj) SetMessage(msg *Message) {
	p.Message = msg
}

func (p *PushObj) SetOptions(opt *Options) {
	p.Options = opt
}
```

---

### 4.5 Dummy推送器实现

**新建文件**: `internal/pusher/offlinepush/dummy/push.go`

```go
package dummy

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/pusher/offlinepush"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type Dummy struct {
	wklog.Log
}

// NewClient 创建Dummy推送器（空实现）
func NewClient() *Dummy {
	return &Dummy{
		Log: wklog.NewWKLog("DummyPush"),
	}
}

// Push 空实现，不执行任何推送
func (d *Dummy) Push(ctx context.Context, userIDs []string, title, content string, opts *offlinepush.PushOptions) error {
	d.Debug("Dummy推送器被调用（不执行实际推送）",
		zap.Int("user_count", len(userIDs)),
		zap.String("title", title),
	)
	return nil
}
```

---

### 4.6 服务层集成

**修改文件**: `internal/service/common.go`

**添加**:

```go
import (
	"github.com/WuKongIM/WuKongIM/internal/pusher/offlinepush"
)

var OfflinePushManager *offlinepush.Manager

func init() {
	// 在现有初始化代码后添加
	OfflinePushManager = offlinepush.NewManager()
}
```

---

### 4.7 离线推送处理器修改

**修改文件**: `internal/pusher/handler/event_pushoffline.go`

**修改后的代码**:

```go
package handler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/pusher/offlinepush"
	"github.com/WuKongIM/WuKongIM/internal/service"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (h *Handler) pushOffline(ctx *eventbus.PushContext) {
	for _, e := range ctx.Events {

		// ========== 1. 原有逻辑：AI推送处理 ==========
		for _, toUid := range e.OfflineUsers {
			fromUid := e.Conn.Uid
			if fromUid != toUid && h.isAI(toUid) && !e.Frame.GetsyncOnce() && !options.G.IsSystemUid(fromUid) {
				h.processAIPush(toUid, e)
			}
		}

		// ========== 2. 新增：内置JPush推送 ==========
		if service.OfflinePushManager != nil && service.OfflinePushManager.Enabled() {
			h.processOfflinePush(e)
		}
	}

	// ========== 3. 原有逻辑：Webhook通知 ==========
	service.Webhook.NotifyOfflineMsg(ctx.Events)
}

// processOfflinePush 处理离线推送
func (h *Handler) processOfflinePush(e *eventbus.Event) {
	sendPacket, ok := e.Frame.(*wkproto.SendPacket)
	if !ok {
		return
	}

	// 过滤掉发送者自己
	offlineUsers := make([]string, 0, len(e.OfflineUsers))
	for _, uid := range e.OfflineUsers {
		if uid != e.Conn.Uid {
			offlineUsers = append(offlineUsers, uid)
		}
	}

	if len(offlineUsers) == 0 {
		return
	}

	// 解析消息内容
	var messageContent map[string]interface{}
	json.Unmarshal(sendPacket.Payload, &messageContent)

	// 构造推送标题和内容
	title := "新消息"
	content := "您有一条新消息"

	if contentText, ok := messageContent["content"].(string); ok {
		content = contentText
	}

	// 转换为推送选项
	opts := offlinepush.ConvertFromEvent(e)

	// 执行推送（使用goroutine异步推送，不阻塞主流程）
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := service.OfflinePushManager.Push(ctx, offlineUsers, title, content, opts)
		if err != nil {
			h.Error("离线推送失败",
				zap.Error(err),
				zap.Int64("message_id", e.MessageId),
				zap.Int("user_count", len(offlineUsers)),
			)
		} else {
			h.Info("离线推送成功",
				zap.Int64("message_id", e.MessageId),
				zap.Int("user_count", len(offlineUsers)),
			)
		}
	}()
}
```

**业务含义**:
1. 保持AI推送逻辑不变
2. 新增内置JPush推送逻辑
3. 保持Webhook通知机制不变（向后兼容）
4. 使用goroutine异步推送，避免阻塞消息分发流程
5. 过滤掉发送者自己，避免自己给自己推送

---

## 五、代码修改清单

| 序号 | 文件路径 | 操作类型 | 行数位置 | 修改内容 | 业务含义 |
|------|---------|---------|---------|---------|---------|
| 1 | `internal/options/options.go` | 新增 | ~200行后 | 添加OfflinePush配置结构 | 支持离线推送配置（JPush、FCM等） |
| 2 | `internal/pusher/offlinepush/interface.go` | 新建 | - | 定义推送器接口 | 统一的推送接口，支持多种推送服务 |
| 3 | `internal/pusher/offlinepush/manager.go` | 新建 | - | 实现推送管理器 | 根据配置选择推送服务，管理推送器生命周期 |
| 4 | `internal/pusher/offlinepush/jpush/push.go` | 新建 | - | JPush推送核心逻辑 | 实现JPush API调用，处理认证和错误 |
| 5 | `internal/pusher/offlinepush/jpush/body/*.go` | 新建 | - | JPush请求体结构 | 封装JPush API的数据结构 |
| 6 | `internal/pusher/offlinepush/dummy/push.go` | 新建 | - | Dummy推送器 | 默认空实现，避免空指针错误 |
| 7 | `internal/service/common.go` | 修改 | init函数 | 注册OfflinePushManager | 全局初始化推送管理器 |
| 8 | `internal/pusher/handler/event_pushoffline.go` | 修改 | 9-22行 | 添加processOfflinePush调用 | 在离线推送流程中集成JPush |

---

## 六、与OpenIM方案对比

| 对比项 | OpenIM Server | WuKongIM（方案B） | 说明 |
|--------|--------------|------------------|------|
| **架构设计** | 推送服务独立，通过Kafka消息队列触发 | 推送逻辑内置在pusher模块中 | WuKongIM更轻量，无需Kafka |
| **推送器接口** | OfflinePusher接口，统一Push方法 | 完全复用OpenIM的接口设计 | 保持一致性 |
| **推送器选择** | 工厂模式，通过配置Enable字段选择 | 完全复用OpenIM的选择逻辑 | 保持一致性 |
| **JPush实现** | 独立的jpush包，body子包封装请求体 | 完全复用OpenIM的实现结构 | 代码结构一致 |
| **配置管理** | Push配置结构，包含多种推送服务配置 | 复用OpenIM的配置设计，适配WuKongIM | 配置结构类似 |
| **用户标识** | 使用别名（Alias）推送 | 使用用户ID作为别名 | 需要客户端绑定别名 |
| **错误处理** | 检查sendno字段（不规范） | 优化为检查error和msg_id字段 | 更符合JPush规范 |
| **异步推送** | 通过Kafka异步 | 通过goroutine异步 | 避免阻塞主流程 |

---

## 七、HTTP客户端实现

由于WuKongIM可能没有与OpenIM相同的HTTP客户端库，我们需要实现一个简单的HTTP客户端。

**新建文件**: `pkg/wkhttp/client.go`

```go
package wkhttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type Client struct {
	httpClient *http.Client
}

// NewClient 创建HTTP客户端
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
			},
			Timeout: 30 * time.Second,
		},
	}
}

// PostJSON 发送POST请求
func (c *Client) PostJSON(ctx context.Context, url string, headers map[string]string, body []byte, timeout int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 检查HTTP状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP错误: %d, body: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}
```

---

## 八、用户别名绑定方案

JPush使用别名（Alias）来标识用户。需要在客户端登录后绑定用户ID和JPush Registration ID的关系。

### 8.1 客户端集成

参考主文档《JPush集成技术方案.md》中的移动端集成章节。

### 8.2 服务端存储方案

**方案1: 扩展用户表**

在用户表中添加字段存储JPush Registration ID（简单场景）。

**方案2: 独立的设备Token表**（推荐）

```sql
CREATE TABLE `user_device_tokens` (
  `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
  `uid` VARCHAR(64) NOT NULL COMMENT '用户ID',
  `platform` VARCHAR(16) NOT NULL COMMENT '平台: ios, android',
  `device_id` VARCHAR(128) NOT NULL COMMENT '设备ID',
  `jpush_registration_id` VARCHAR(256) NOT NULL COMMENT 'JPush Registration ID',
  `status` TINYINT DEFAULT 1 COMMENT '状态: 1-有效 0-无效',
  `created_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY `uk_device` (`device_id`),
  KEY `idx_uid` (`uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 8.3 JPush别名绑定

在客户端获取到JPush Registration ID后，调用JPush的别名设置API：

```go
// 方式1: 客户端直接设置别名（需要JPush SDK支持）
// JPush SDK提供了设置别名的方法

// 方式2: 服务端设置别名（通过JPush Server API）
// POST https://api.jpush.cn/v3/devices/registration_id/{registration_id}/alias
```

推荐使用客户端直接设置别名的方式，更简单高效。

---

## 九、实施步骤

### 步骤1: 准备工作

1. **获取JPush凭证**
   - 注册极光推送账号: https://www.jiguang.cn/
   - 创建应用，获取AppKey和MasterSecret

2. **准备开发环境**
   - 确保Go版本>=1.20
   - 安装必要的依赖

### 步骤2: 创建目录结构

```bash
cd /Users/changzechuan/WenchuanProjects/IMProjects/WuKongIM

# 创建离线推送目录
mkdir -p internal/pusher/offlinepush/jpush/body
mkdir -p internal/pusher/offlinepush/dummy
mkdir -p pkg/wkhttp
```

### 步骤3: 创建代码文件

按照第四章的代码示例，依次创建以下文件：

1. `pkg/wkhttp/client.go` - HTTP客户端
2. `internal/pusher/offlinepush/interface.go` - 推送器接口
3. `internal/pusher/offlinepush/manager.go` - 推送管理器
4. `internal/pusher/offlinepush/jpush/push.go` - JPush推送器
5. `internal/pusher/offlinepush/jpush/body/*.go` - JPush请求体
6. `internal/pusher/offlinepush/dummy/push.go` - Dummy推送器

### 步骤4: 修改现有文件

1. 修改 `internal/options/options.go` - 添加配置结构
2. 修改 `internal/service/common.go` - 注册推送管理器
3. 修改 `internal/pusher/handler/event_pushoffline.go` - 集成推送调用

### 步骤5: 配置文件修改

修改 `config/wk.yaml` 或 `exampleconfig/cluster1.yaml`：

```yaml
offlinePush:
  enable: "jpush"
  jpush:
    appKey: "your_jpush_appkey"
    masterSecret: "your_master_secret"
    pushURL: "https://api.jpush.cn/v3/push"
    pushIntent: "#Intent;component=com.example.app/.MainActivity;end"
  iosPush:
    pushSound: "default"
    badgeCount: true
    production: false
```

### 步骤6: 编译和测试

```bash
# 编译项目
go build -o wukongim main.go

# 运行项目
./wukongim --config config/wk.yaml

# 检查日志，确认推送管理器已启动
# 应该看到类似日志:
# [OfflinePushManager] 创建离线推送器 service=jpush
# [OfflinePushManager] 使用JPush推送服务
```

### 步骤7: 集成测试

1. **准备测试用户**
   - 用户A: 登录并绑定JPush别名
   - 用户B: 登录后断开连接（模拟离线）

2. **测试推送流程**
   ```bash
   # 用户A给用户B发送消息
   curl -X POST http://localhost:5001/message/send \
     -H "Content-Type: application/json" \
     -d '{
       "from_uid": "userA",
       "channel_id": "userB",
       "channel_type": 1,
       "payload": "SGVsbG8gV29ybGQ="
     }'
   ```

3. **验证结果**
   - 检查WuKongIM日志，确认推送已发送
   - 检查用户B的移动设备，确认收到推送通知
   - 检查JPush控制台，查看推送统计

---

## 十、测试方案

### 10.1 单元测试

**创建文件**: `internal/pusher/offlinepush/jpush/push_test.go`

```go
package jpush

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/pusher/offlinepush"
)

func TestJPush_Push(t *testing.T) {
	// 注意：需要配置有效的JPush凭证
	client := NewClient()

	opts := &offlinepush.PushOptions{
		MessageID:     123456,
		ClientMsgNo:   "test_msg_001",
		ChannelID:     "testChannel",
		ChannelType:   1,
		FromUID:       "testUser",
		IOSPushSound:  "default",
		IOSBadgeCount: true,
	}

	err := client.Push(
		context.Background(),
		[]string{"test_user_1", "test_user_2"},
		"测试标题",
		"测试内容",
		opts,
	)

	if err != nil {
		t.Fatalf("推送失败: %v", err)
	}

	t.Log("推送成功")
}
```

### 10.2 集成测试脚本

**创建文件**: `test/jpush_integration_test.sh`

```bash
#!/bin/bash

BASE_URL="http://localhost:5001"

echo "=== JPush集成测试 ==="

echo "1. 发送测试消息（用户A → 离线用户B）"
curl -X POST $BASE_URL/message/send \
  -H "Content-Type: application/json" \
  -d '{
    "from_uid": "userA",
    "channel_id": "userB",
    "channel_type": 1,
    "payload": "eyJ0eXBlIjoidGV4dCIsImNvbnRlbnQiOiLov5nmmK/kuIDmnaHmtYvor5Xmtojmga8ifQ=="
  }'

echo "\n2. 检查推送日志"
echo "请查看WuKongIM日志，确认推送已发送"

echo "\n3. 验证JPush控制台"
echo "登录 https://www.jiguang.cn/ 查看推送统计"
```

---

## 十一、监控和告警

### 11.1 监控指标

参考主文档《JPush集成技术方案.md》中的监控章节。

### 11.2 日志规范

```go
// 推送成功日志
h.Info("离线推送成功",
    zap.Int64("message_id", e.MessageId),
    zap.Int("user_count", len(offlineUsers)),
    zap.String("channel_id", e.ChannelId),
)

// 推送失败日志
h.Error("离线推送失败",
    zap.Error(err),
    zap.Int64("message_id", e.MessageId),
    zap.Int("user_count", len(offlineUsers)),
    zap.String("channel_id", e.ChannelId),
)
```

---

## 十二、常见问题

### Q1: 如何切换推送服务商？

**A**: 修改配置文件中的 `offlinePush.enable` 字段：

```yaml
offlinePush:
  enable: "jpush"   # 改为 "fcm" 或 "dummy"
```

### Q2: 推送失败如何调试？

**A**:
1. 检查配置文件中的AppKey和MasterSecret是否正确
2. 检查用户是否已绑定JPush别名
3. 查看WuKongIM日志中的推送请求和响应
4. 登录JPush控制台查看推送记录和错误信息

### Q3: 如何支持多种推送服务商并存？

**A**: 当前设计为单一推送服务，如需同时使用多种推送服务，需要修改Manager逻辑：

```go
// 修改为支持多推送器
type Manager struct {
    pushers []OfflinePusher
}

func (m *Manager) Push(...) error {
    // 遍历所有推送器，并发推送
    for _, pusher := range m.pushers {
        go pusher.Push(...)
    }
}
```

### Q4: 推送内容如何加密？

**A**:
1. WuKongIM的Payload字段已经是Base64编码
2. 如需端到端加密，在App端解密
3. JPush的通知内容（alert）建议只推送摘要，具体内容在App内显示

---

## 十三、性能优化建议

### 13.1 批量推送优化

JPush支持单次推送1000个别名，可以优化为批量推送：

```go
// 将离线用户分批，每批1000个
const batchSize = 1000
for i := 0; i < len(offlineUsers); i += batchSize {
    end := i + batchSize
    if end > len(offlineUsers) {
        end = len(offlineUsers)
    }
    batch := offlineUsers[i:end]

    go service.OfflinePushManager.Push(ctx, batch, title, content, opts)
}
```

### 13.2 缓存用户Token

将用户的JPush Registration ID缓存到Redis，减少数据库查询：

```go
// 从Redis获取用户Token
cacheKey := fmt.Sprintf("jpush:token:%s", uid)
token, err := redis.Get(cacheKey)
if err == nil {
    return token
}

// Redis没有，从数据库查询
token = db.GetUserJPushToken(uid)

// 存入Redis缓存
redis.Set(cacheKey, token, time.Hour*24)
```

### 13.3 异步推送队列

对于大量推送，可以引入推送队列：

```go
type PushTask struct {
    UserIDs []string
    Title   string
    Content string
    Opts    *PushOptions
}

var pushQueue = make(chan *PushTask, 10000)

// 消费者goroutine
go func() {
    for task := range pushQueue {
        service.OfflinePushManager.Push(ctx, task.UserIDs, task.Title, task.Content, task.Opts)
    }
}()

// 生产者：将推送任务放入队列
pushQueue <- &PushTask{
    UserIDs: offlineUsers,
    Title:   title,
    Content: content,
    Opts:    opts,
}
```

---

## 十四、总结

### 14.1 方案优势

| 优势 | 说明 |
|------|------|
| ✅ 复用成熟方案 | 基于OpenIM Server的成功经验，降低开发风险 |
| ✅ 性能优势 | 无需额外HTTP请求，推送延迟更低 |
| ✅ 简化部署 | 无需部署独立服务，运维成本低 |
| ✅ 易于扩展 | 基于接口设计，轻松添加新的推送服务商 |
| ✅ 向后兼容 | 保留Webhook机制，不影响现有功能 |

### 14.2 实施建议

1. **分阶段实施**
   - 第一阶段：搭建基础框架，实现JPush推送
   - 第二阶段：完善用户Token管理和数据库设计
   - 第三阶段：优化推送性能，添加监控告警
   - 第四阶段：支持多推送服务商（FCM、个推等）

2. **风险控制**
   - 生产环境先小范围灰度测试
   - 保留详细的推送日志，便于问题排查
   - 监控推送成功率和延迟

3. **技术演进**
   - 持续优化推送性能
   - 支持更多推送服务商
   - 实现智能推送策略

### 14.3 与方案A对比

| 对比项 | 方案A（Webhook集成） | 方案B（内置集成） |
|--------|---------------------|------------------|
| **开发复杂度** | 低（无需修改核心代码） | 中（需修改6-7个文件） |
| **运维复杂度** | 高（需部署中间服务） | 低（无需额外服务） |
| **推送延迟** | 稍高（多一次HTTP请求） | 低（直接调用） |
| **扩展性** | 高（易于切换推送服务） | 中（需修改代码） |
| **监控集成** | 分散（WuKongIM+中间服务） | 统一（集成在WuKongIM） |
| **推荐场景** | 生产环境、需要灵活配置 | 简单场景、对性能要求高 |

---

## 附录：参考资料

### OpenIM Server 相关

- **OpenIM GitHub**: https://github.com/openimsdk/open-im-server
- **OpenIM JPush实现**: `/Users/changzechuan/IMProjects/open-im-server/internal/push/offlinepush/jpush/`
- **OpenIM JPush文档**: `/Users/changzechuan/IMProjects/open-im-server/USE_JPush.md`

### WuKongIM 相关

- **WuKongIM GitHub**: https://github.com/WuKongIM/WuKongIM
- **WuKongIM官方文档**: https://githubim.com
- **离线推送处理**: `/Users/changzechuan/WenchuanProjects/IMProjects/WuKongIM/internal/pusher/handler/event_pushoffline.go`

### JPush 相关

- **JPush官方文档**: https://docs.jiguang.cn/jpush/guideline/intro/
- **JPush REST API**: https://docs.jiguang.cn/jpush/server/push/rest_api_v3_push
- **JPush iOS SDK**: https://docs.jiguang.cn/jpush/client/iOS/ios_sdk
- **JPush Android SDK**: https://docs.jiguang.cn/jpush/client/Android/android_sdk

---

**文档版本**: v1.0
**创建日期**: 2025-10-18
**最后更新**: 2025-10-18
**维护人**: Claude Code
**审核状态**: 待审核