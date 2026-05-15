# proxy-hub 架构方案

## 目标速览

Go 单体二进制；SOCKS5 代理延迟测试与 IP 回显测试分离；带鉴权中文前端；JSON 文件持久化到 `/data`；最近一次测试结果保留为快照；模块化预留 HTTP/HTTPS、对外代理 API；Zeabur 通过 GitHub 源码部署并挂载 `/data`；前端资源与 API 使用相对路径以适配 Caddy `handle_path /xxx/*` 前缀剥离。

## 技术选型

| 维度 | 选型 | 理由 |
|------|------|------|
| 后端 | Go 1.26，`net/http` 标准库 ServeMux（`GET /path/{id}` 模式自 1.22 起原生支持） | 轻量，无 cgo，无第三方路由依赖 |
| 前端 | 原生 HTML/CSS/JS + Alpine.js 本地文件 | 无构建；不依赖外部 CDN；可随 `embed.FS` 进入单二进制 |
| 资源嵌入 | `embed.FS` | 单二进制部署 |
| 存储 | JSON 文件 + 进程内 RWMutex；写入用临时文件 + rename 原子替换 | `/data` 体量小、读多写少；不引入 SQLite |
| 鉴权 | Cookie + HMAC token（包含 `sha256(ADMIN_KEY)` 前缀）；服务启动期计算基准 hash | 登录长期有效；`ADMIN_KEY` 变更后旧 cookie 失效 |
| 登录保护 | 内存失败计数 + 短时间延迟/429 | Caddy 已做 IP 白名单，应用内只做基础爆破防护 |
| 日志 | 自实现环形缓冲（最近 N 条）+ 可选文件落盘到 `/data/logs/app.log` | 内存上限固定；日志内容脱敏 |
| 路由 | 前端用 `./app.js`、`./api/...`；后端注册 `/api/...` | Caddy `handle_path` 剥离前缀后仍可访问 |

## 依赖与锁定策略

- 后端优先只使用 Go 标准库；非必要不引入第三方 Go 模块。
- 如引入第三方 Go 模块，必须提交 `go.mod` 与 `go.sum`，构建使用 `go mod download`/`go build` 的校验机制。
- 前端不使用 npm 构建链，不提交 `package.json`；Alpine.js 作为固定版本文件放入 `web/vendor/alpine.min.js` 并随二进制嵌入。
- 不从 CDN 动态加载运行时代码，避免运行时供应链变化。
- Docker 基础镜像固定到明确版本；发布前可进一步固定 digest。
- 禁止在 Zeabur 构建阶段执行未锁定版本的安装命令，例如 `npm install`、`go get latest`。

## 目录结构

```
proxy-hub/
├── cmd/server/main.go              # 入口；装配 Container
├── internal/
│   ├── config/                     # 环境变量、运行期设置加载/落盘
│   │   ├── env.go                  # ADMIN_KEY / DATA_DIR / LISTEN
│   │   └── settings.go             # 用户可改设置（持久化到 /data/settings.json）
│   ├── auth/
│   │   ├── auth.go                 # 登录、token 签发/校验、中间件
│   │   ├── limiter.go              # 登录失败基础限速
│   │   └── token.go                # HMAC + admin_key_hash 绑定
│   ├── store/
│   │   ├── store.go                # interface Store{Groups,Results,Settings}
│   │   └── jsonfile.go             # 原子写实现
│   ├── proxy/                      # 代理模型 + 解析器
│   │   ├── model.go                # Proxy{Scheme,Host,Port,User,Pass,Raw}
│   │   └── parser.go               # 多格式解析（见下）
│   ├── prober/                     # 协议探测器，可拓展
│   │   ├── prober.go               # interface Prober{Probe(ctx, p) Result}
│   │   ├── socks5.go               # 当前实现
│   │   ├── http.go                 # 占位 TODO
│   │   └── registry.go             # scheme -> Prober
│   ├── tester/
│   │   ├── tester.go               # 延迟测试 worker pool
│   │   ├── echo.go                 # IP 回显测试，多源备用
│   │   └── jobs.go                 # 测试任务与 SSE 订阅
│   ├── api/
│   │   ├── router.go               # 装配所有路由
│   │   ├── handler_groups.go
│   │   ├── handler_test.go
│   │   ├── handler_settings.go
│   │   ├── handler_logs.go
│   │   └── handler_auth.go
│   ├── logbuf/
│   │   └── ring.go                 # 环形 buffer + 可选 file sink
│   └── version/version.go
├── web/                            # embed 进二进制
│   ├── index.html
│   ├── login.html
│   ├── app.js
│   ├── app.css
│   └── vendor/alpine.min.js
├── docs/plan.md
├── Dockerfile                      # 多阶段，distroless
└── go.mod
```

## 模块边界与依赖方向

```
api  →  tester(jobs)  →  prober(registry → socks5/http/...)
 │          │                 │
 │          └→ echo           └→ proxy(model/parser)
 ├→ store ─────── 持久化 groups/results/settings
 ├→ auth  ─────── 鉴权与登录限速
 ├→ config ────── 环境变量与运行期 settings
 └→ logbuf ────── 脱敏日志输出
```
单向依赖；底层不反向引用 api。新增协议 = 实现 `Prober` + `registry.Register("http", ...)`。

## 鉴权流程（"登录永久有效，ADMIN_KEY 变则失效"）

```
启动:   baseHash = sha256(env.ADMIN_KEY)[:8]
登录:   POST /api/auth/login {key}
        if sha256(key) == sha256(ADMIN_KEY): 签发 cookie
        cookie = base64(payload) + "." + HMAC(serverSecret, payload)
        payload = {hashPrefix: baseHash, iat: now}   # 无 exp
中间件: 解析 cookie → 校验 HMAC → 校验 hashPrefix == baseHash
        不匹配 → 401
serverSecret:  /data/.secret 首次启动生成 32B 随机值并落盘
```

比较规则：
- 登录密钥比较使用 `subtle.ConstantTimeCompare`。
- HMAC 签名校验使用 `hmac.Equal`。
- `baseHash` 用于判断 `ADMIN_KEY` 是否变更，不用于替代 HMAC 签名。

Cookie 属性：
- `HttpOnly=true`
- `SameSite=Lax`
- `Secure` 在请求为 HTTPS 或配置开启时启用
- Cookie Path 固定写 `/`，适配任意 Caddy 前缀；禁止使用浏览器默认路径
- `Max-Age` 设置为长期有效；token 本身无过期时间，`ADMIN_KEY` 变更后仍会失效
- `Name=proxy_hub_session`

说明：Cookie Path 是服务端写入 Cookie 时给浏览器的规则，不是环境变量。它只决定浏览器访问哪些 URL 时会自动带上这个 Cookie，不是安全边界。这里固定写 `/`，表示同一域名下所有路径都会带上登录 Cookie。使用 Caddy `handle_path /ph/*` 时，Go 服务收到的是剥离前缀后的 `/api/...`，服务端无法可靠知道外部前缀。固定写 `/` 最灵活，可同时适配 `/ph/`、`/proxy-hub/` 或根路径部署。

登录失败保护：
- 按客户端 IP 记录失败次数，默认 5 次失败后短时间返回 `429`
- 仅在 `TRUST_PROXY_HEADERS=1` 时读取 `X-Forwarded-For`，否则使用 `RemoteAddr`
- 登录成功后清空该 IP 的失败计数

要点：token 无 exp，达到长期登录；`ADMIN_KEY` 变更后 `baseHash` 变更，旧 cookie payload 校验失败。

## 数据模型

```go
type Group struct {
    ID      string    `json:"id"`
    Name    string    `json:"name"`
    Created time.Time `json:"created"`
    Proxies []Proxy   `json:"proxies"`
}

type Proxy struct {
    ID     string `json:"id"`
    Label  string `json:"label,omitempty"` // 用户命名，可缺省
    Scheme string `json:"scheme"`          // socks5/http/https
    Host   string `json:"host"`
    Port   int    `json:"port"`
    User   string `json:"user,omitempty"`
    Pass   string `json:"pass,omitempty"`
    Raw    string `json:"raw"`             // 原始字符串，复制时用
}

// 仅保存最近一次快照，不保存历史曲线。
type ProxyResult struct {
    ProxyID string         `json:"proxy_id"`
    Latency *LatencyResult `json:"latency,omitempty"`
    Echo    *EchoResult    `json:"echo,omitempty"`
}

type LatencyResult struct {
    OK        bool      `json:"ok"`
    LatencyMs int       `json:"latency_ms"`
    TestURL   string    `json:"test_url"`
    TestedAt  time.Time `json:"tested_at"`
    Error     string    `json:"error,omitempty"`
}

type EchoResult struct {
    OK              bool      `json:"ok"`
    EchoIP          string    `json:"echo_ip,omitempty"`
    CountryCode     string    `json:"country_code,omitempty"`
    Country         string    `json:"country,omitempty"`
    Region          string    `json:"region,omitempty"`
    City            string    `json:"city,omitempty"`
    Postal          string    `json:"postal,omitempty"`
    ContinentCode   string    `json:"continent_code,omitempty"`
    ASN             int       `json:"asn,omitempty"`
    ASNOrganization string    `json:"asn_organization,omitempty"`
    Organization    string    `json:"organization,omitempty"`
    ISP             string    `json:"isp,omitempty"`
    Latitude        float64   `json:"latitude,omitempty"`
    Longitude       float64   `json:"longitude,omitempty"`
    Timezone        string    `json:"timezone,omitempty"`
    Source          string    `json:"source,omitempty"`
    TestedAt        time.Time `json:"tested_at"`
    Error           string    `json:"error,omitempty"`
}
```

代理 ID 与去重：
- `ID` 创建时由 `crypto/rand` 生成 128-bit 随机值，hex 编码。
- 批量追加去重使用 `scheme+host+port+user+pass` 生成比较 key，不使用 `ID`。
- 编辑代理 `raw` 后重新解析，保留原 `ID`，并清理该代理旧测试结果。

落盘文件：
```
/data/
├── .secret                 # HMAC key
├── settings.json
├── groups.json             # 全部组（量小，单文件即可）
├── results.json            # 最近一次延迟/IP 回显快照
└── logs/app.log            # 可选
```

测试结果策略：
- 延迟结果和 IP 回显结果分开保存，互不覆盖。
- 结果只表示某个时间点的最近快照，前端必须展示测试时间。
- 刷新页面、切换标签页、服务重启后可恢复最近快照。
- 删除代理或替换整组后，清理不存在代理对应的结果。
- 批量测试运行期间结果先写入内存，任务完成后统一覆盖写入 `/data/results.json`。
- 不保存历史曲线；如后续需要趋势分析，再增加独立历史文件或数据库。

## 代理字符串解析（parser.go）

```
预处理: trim, 去 scheme（默认 socks5）, 记录原文存 Raw

按优先级匹配：
1) scheme://[user:pass@]host:port              # 完整 URL
2) user:pass@host:port                          # @ 在 cred 与 host 之间
3) host:port@user:pass                          # 反向 @
4) host:port:user:pass                          # 供应商常见 4 段格式
5) user:pass:host:port                          # 供应商常见 4 段格式
6) host:port                                    # 无凭据

注意：密码内含冒号会冲突 → 仅在严格匹配失败后回退；解析失败行返给前端展示，不中断其他行。
```

解析规则：
- URL 格式优先使用 `net/url`；特殊字符必须 URL 编码，例如密码中的 `@`、`:`。
- `host:port` 使用 `net.SplitHostPort` 或等价严格解析，端口必须是 1-65535。
- IPv6 仅在标准 URL 或 `[ipv6]:port` 格式下支持，不支持 `a:b:c:d` 简写歧义格式。
- 4 段冒号格式按端口位置解析：第二段是合法端口时按 `host:port:user:pass`；否则第四段是合法端口时按 `user:pass:host:port`。
- 如果第二段和第四段都像端口，该行存在歧义，返回解析失败并提示改用 `user:pass@host:port` 或 `socks5://user:pass@host:port`。
- 批量导入/追加默认按 `scheme+host+port+user+pass` 精确去重；替换整组不保留未出现在输入中的旧代理。

## 探测器（socks5.go 核心思路）

```
Probe(ctx, p):
  start = now
  dial TCP p.Host:p.Port (DialContext + timeout)
  SOCKS5 greet  → method nego (0x00 / 0x02 auth)
  if user/pass:  username/password subnegotiation (RFC1929)
  CONNECT target = testURL.host:testURL.port → ATYP=0x03 远端解析域名，期望 0x00 reply
  if testURL.scheme == https:
      在隧道上做 TLS 握手（ServerName=testURL.host）
      发送 HTTP GET testURL.path
  if testURL.scheme == http:
      在隧道上发送明文 HTTP GET testURL.path
  默认 URL 期望 status 204；自定义 URL 默认 2xx 成功，可配置 expected_status
  latency = now - start
  return Result
```

DNS 规则：
- 连接 SOCKS5 网关本身时，`p.Host` 由运行环境解析，因为必须先连上代理服务器。
- 代理内访问测试 URL 时，默认把域名通过 SOCKS5 `ATYP=0x03` 交给代理服务器解析。
- 这相当于 curl 等客户端里的 `socks5h` 行为。
- 如果代理明确返回不支持域名地址类型或域名连接失败，回退为本地解析 DNS，再用 `ATYP=0x01/0x04` 连接解析出的 IP。
- 回退结果仍可判定代理可用，但测试结果中标记 `dns_mode=remote|local_fallback`，便于识别供应商能力差异。
- 不在设置页增加 DNS 开关，避免把内部兼容细节暴露给日常使用。

IP 回显测试独立执行：
```
Echo(ctx, p):
  通过代理请求 ipinfo.io/json
  失败后按顺序使用备用源: api.ip.sb/geoip → ipwho.is
  返回出口 IP、国家/地区、ASN、组织、ISP、经纬度、时区、来源站点、错误信息
```

IP 回显源：
- `https://ipinfo.io/json`：`ip` → `echo_ip`；`country` → `country_code`；`region/city/postal/timezone` 直接映射；`loc` 按 `lat,lon` 解析；`org` 按 `AS<asn> <asn_org>` 解析到 `asn/asn_organization`，同时用解析出的组织名填充 `organization`。
- `https://api.ip.sb/geoip`：`ip/country_code/country/organization/isp/asn/asn_organization/latitude/longitude/timezone/continent_code` 直接映射。
- `https://ipwho.is/`：`success=false` 视为失败；`ip/country_code/country/region/city/postal/latitude/longitude/continent_code` 直接映射；`connection.asn/org/isp` 映射到 `asn/organization/isp`；`connection.org` 同时作为 `asn_organization`；`timezone.id` 映射到 `timezone`。
- 不配置 token；每个源设置独立短超时，失败后立即切换下一个源。
- 速率限制按回显服务看到的出口 IP 计算；多个代理共用同一出口时可能触发限制，触发后依靠备用源继续测试。
- IP 回显完整响应 JSON 不写入日志、不通过 `/api/logs` 返回；解析后的必要字段保存到结果快照。

自定义测试 URL：
- 固定使用 `GET` 请求。
- 设置页提示不要填写会产生副作用的接口。
- 默认以 `2xx` 判定成功，也允许指定期望状态码。

IO 约束：
- `net.Dialer.DialContext` 负责连接阶段取消。
- 每次读写前设置 `Conn.SetDeadline`，避免握手和 HTTP 读写在 ctx 取消后悬挂。
- `defer Conn.Close()` 必须覆盖所有返回路径。
- 批量测试使用 worker pool 限制并发，不因单个代理失败中断其他代理。

## 前端

### 路由 & 资源加载
- `index.html` / `login.html` 内全部资源用相对路径（`./app.js`、`./api/...`）。
- Caddy `handle_path /ph/*` 剥前缀后直达 Go 服务根，不影响。
- 页面文案使用中文；内部 API 字段保持英文。

### ASCII 模拟

#### 登录页
```
┌────────────────────────────────────────────┐
│              proxy-hub                     │
│  ┌──────────────────────────────────────┐  │
│  │  管理密钥: [********************]    │  │
│  │  [ 登录 ]                            │  │
│  └──────────────────────────────────────┘  │
└────────────────────────────────────────────┘
```

#### 主标签页
```
┌─ proxy-hub ────────────────────────────────────[ 代理 | 设置 | 日志 ]──────┐
│ 分组: [ US-sk5 ▾ ] [+ 新建分组] [ 重命名 ] [ 删除 ]                       │
│ 当前分组: US-sk5                                                          │
│ [ + 添加 ] [ 批量编辑 ] [ 延迟测试 ] [ IP回显 ] [ 导出 ] [ 复制本组全部 ] │
│ 选中: 24  成功: 18  失败: 6   最近测试: 2026-05-15 21:09:12             │
│ ┌─────────────────────────────────────────────────────────────────────┐   │
│ │ 名称       │ 协议   │ Host:Port             │ 延迟  │ 出口IP/结果   │   │
│ │ kookeey-01 │ socks5 │ gate.kookeey.info:10..│  87ms │ 72.1.179.136 US│   │
│ │ —          │ socks5 │ 1.2.3.4:1080          │ 152ms │ 8.8.8.8 US     │   │
│ │ —          │ socks5 │ bad.host:1080         │  —    │ 超时（悬停看详情）│ │
│ │ ...                                                  │ [▶] [⧉]       │   │
│ └─────────────────────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────────────────────┘
延迟着色: ≤100ms 绿  101~200ms 黄  >200ms 红   错误: 红色，悬停显示全文
```

#### 批量编辑/导入弹窗
```
┌─ 批量编辑 (US-sk5) ──────────────────────────┐
│ 每行一个代理。支持格式:                      │
│  socks5://ip:port                            │
│  socks5://user:pass@ip:port                  │
│  host:port:user:pass                         │
│  host:port@user:pass                         │
│  user:pass:host:port                         │
│  user:pass@host:port                         │
│ ┌──────────────────────────────────────────┐ │
│ │ 4135349-...US-31234168@gate.kookeey:1000 │ │
│ │ socks5://1.2.3.4:1080                    │ │
│ │ ...                                      │ │
│ └──────────────────────────────────────────┘ │
│ [ 替换整组 ] [ 追加去重 ]         [ 取消 ]   │
└──────────────────────────────────────────────┘
```

导出与复制：
- 导出本组：生成 `text/plain`，每行使用代理 `Raw` 原文。
- 复制本组全部：前端从当前组数据生成同样文本并写入剪贴板。
- 单条复制：复制该代理 `Raw` 原文。

#### 设置标签页
```
┌─ 设置 ────────────────────────────────────────────────────────────────┐
│ 并发数:                [ 20 ]                                          │
│ 单代理超时:            [ 5000 ] ms                                     │
│ 延迟测试 URL:          ( ) https://www.google.com/generate_204         │
│                        (•) https://cp.cloudflare.com/generate_204      │
│                        ( ) 自定义: [____________________]              │
│ 自定义 URL:            固定 GET；不要填写会产生副作用的接口            │
│ 自定义 URL 成功规则:   [ 2xx ▾ ] 或指定状态码 [ 204 ]                 │
│ 低延迟阈值:            [ 100 ] ms                                      │
│ 日志:                                                                  │
│   写入文件:            [x] 启用                                        │
│   最大大小:            [ 3 ] MB                                        │
│ [ 保存 ]                                                               │
└────────────────────────────────────────────────────────────────────────┘
```

#### 日志标签页
```
┌─ 日志（最近 100 条）────────────────────────────[ 自动刷新: 2s ]──────┐
│ 21:09:12 INFO  延迟测试 group=US-sk5 host=gate.kookeey... lat=87ms ok │
│ 21:09:12 WARN  IP回显备用源 ipinfo→ip.sb host=1.2.3.4                │
│ 21:09:11 ERR   socks5 认证失败 host=bad.host:1080                    │
│ ...                                                                   │
└───────────────────────────────────────────────────────────────────────┘
```

## HTTP API（前端相对调用）

```
/api/auth/login                 POST    {key} → 写入 cookie
/api/auth/logout                POST
/api/me                         GET     探活 / 校验 cookie
/healthz                        GET     健康检查，不需要鉴权

/api/groups                     GET     列出
/api/groups                     POST    {name}
/api/groups/{id}                PATCH   {name}
/api/groups/{id}                DELETE

/api/groups/{id}/proxies        POST    {raw}                         # 单条原文
/api/groups/{id}/proxies/bulk   POST    {text, mode}                  # mode=replace|append，append 精确去重
/api/groups/{id}/proxies/{pid}  PATCH   {label?, raw?}                # raw 变更时重新解析
/api/groups/{id}/proxies/{pid}  DELETE
/api/groups/{id}/export         GET     text/plain，每行 Raw 原文

/api/test/jobs                  POST    {kind, group_id, proxy_ids?}  # kind=latency|echo
/api/test/jobs/{id}/stream      GET     SSE 流式返回结果
/api/test/one                   POST    {kind, raw}                   # 即席测试，不入库
/api/results                    GET     ?group_id=...

/api/settings                   GET
/api/settings                   PUT

/api/logs                       GET     ?limit=100
/api/logs/stream                GET     SSE，可选
```

批量测试使用任务模型：
- `POST /api/test/jobs` 只创建任务并返回 `job_id`，不直接占用长请求。
- `GET /api/test/jobs/{id}/stream` 使用原生 `EventSource` 订阅进度。
- SSE 断开不取消任务；任务完成后结果写入 `/data/results.json`。
- job manager 保留运行中任务的当前状态，刷新页面后可重新订阅同一任务。
- 不实现完整 `Last-Event-ID` 事件重放；最终状态以 `/api/results` 快照为准。
- 前端切换标签页不得清空运行中任务和最近结果。

## 部署

### Zeabur GitHub 源码部署

- 仓库推送到 GitHub 后由 Zeabur 自动识别并构建。
- 仓库保留 `Dockerfile`，Zeabur 优先按 Dockerfile 构建，避免平台猜测 Go 项目入口。
- Zeabur 控制台挂载 Volume 到 `/data`；应用不依赖 `zeabur.toml` 声明卷。
- 应用启动时必须检查 `/data` 可写，无法写入 `.secret`、`settings.json`、`groups.json` 时直接启动失败并输出明确错误。
- JSON 存储只支持单实例写入，Zeabur 副本数固定为 1。

### 环境变量

| 名称 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `ADMIN_KEY` | 是 | 无 | 登录管理密钥；变更后旧登录 Cookie 自动失效 |
| `DATA_DIR` | 否 | `/data` | 持久化目录；Zeabur Volume 挂载到 `/data` 时保持默认 |
| `LISTEN` | 否 | `:8080` | HTTP 监听地址 |
| `TRUST_PROXY_HEADERS` | 否 | `0` | 设置为 `1` 时登录限速使用 `X-Forwarded-For` 获取客户端 IP |

说明：
- `ADMIN_KEY` 必须在 Zeabur 环境变量中配置，不能使用默认值。
- Cookie Path 固定由程序写为 `/`，不是环境变量。
- `TRUST_PROXY_HEADERS=1` 只应在请求必定经过可信 Caddy 反代时启用。

### Dockerfile（要点）
```
FROM golang:1.26-alpine AS build
... CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxy-hub ./cmd/server
RUN mkdir -p /out/data

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/proxy-hub /proxy-hub
COPY --from=build --chown=65532:65532 /out/data /data
ENV LISTEN=:8080 DATA_DIR=/data
VOLUME ["/data"]
USER 65532:65532
ENTRYPOINT ["/proxy-hub"]
```

### 与用户 Caddy 配合
- 在 Caddyfile 增加：
  ```
  redir /ph /ph/ 308
  handle_path /ph/* {
      reverse_proxy proxy-hub.zeabur.internal:8080
  }
  ```
- 因前端走相对路径，无需 `<base>` 注入。
- Cookie Path 固定写 `/`，不绑定 `/ph`。

## 内存与稳定性

- 环形 log buffer 固定大小（如 100 条 × 1KB ≈ 100KB）。
- 测试结果只保留最近快照，文件覆盖写入 `/data/results.json`。
- 批量测试运行期间不按单条结果频繁写盘，任务完成后统一写入结果快照。
- 每次批测 `context.WithTimeout` + worker 数量限制；探测 `Conn` `defer Close`。
- 文件写采用 `os.WriteFile(tmp) → os.Rename`，避免半写损坏；读时持读锁。
- JSON 存储只面向单进程；部署层固定单副本，禁止多实例同时写 `/data/groups.json`。
- SSE handler 监听 `r.Context().Done()` 仅取消订阅，不取消已创建的测试任务。
- 运行中任务由 job manager 持有；任务完成后释放明细，只保留最近结果快照。
- 日志写入前脱敏 `user/pass/raw/key/cookie` 等敏感字段；代理原文只允许记录为 `scheme://***@host:port` 或 `host:port:***`。
- 日志记录 IP 回显摘要字段，例如 `source=ipinfo ip=72.1.179.136 country_code=US asn=20115 asn_org="Charter Communications LLC" org="Charter Communications LLC" lat=39.1795 lon=-84.3352`；禁止记录回显服务返回的完整 JSON。

## 拓展点（仅留接口，不实现）

- `prober.Prober` 新增 http/https 实现 → 注册 scheme。
- 对外代理端口/API 转发不创建占位目录；真正需要时再新增 `internal/forward/`。
- store 后续可换 SQLite：实现同一 `Store` 接口即可，API 层零改动。
