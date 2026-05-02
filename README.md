# One-picture-API

基于 Go 的轻量随机图服务，支持：
- PC / 手机随机图接口
- 302 跳转静态资源（CDN 友好）
- JSON 返回静态图片 URL
- Token 登录后上传（会话鉴权）
- 图片标签（tag）索引与按标签随机
- 后台标签管理页面（查看图片、覆盖/追加标签）
- 上传页拖拽选择 + 点击上传 + 可附加标签
- 接口调用统计与定时落盘

---

## 当前接口

### 随机图（302 跳转）
- `/api/web`：随机 PC 图（302 到 `/images/...`）
- `/api/m`：随机手机图（302 到 `/images/...`）
- `/api/web?tag=anime`：按标签随机 PC 图
- `/api/m?tag=anime`：按标签随机手机图

### JSON
- `/api/web/json`：返回 PC 图静态 URL
- `/api/m/json`：返回手机图静态 URL
- `/api/web/json?tag=anime`：按标签返回 PC 图 URL
- `/api/m/json?tag=anime`：按标签返回手机图 URL

### 登录鉴权
- `POST /api/login`：提交 token 登录，写入 HttpOnly 会话 Cookie
- `POST /api/logout`：登出并清理会话
- `GET /api/auth/status`：查看当前是否登录

### 上传（需登录）
- `POST /api/upload`
- 表单字段：
  - `file`：图片文件
  - `category`：`web` 或 `m`
  - `tags`：可选，逗号分隔（如 `anime,girl,night`）
- 说明：
  - 不再通过表单 token 上传
  - 必须先登录再上传

### 标签管理（需登录）
- `GET /api/admin/tags`：返回标签列表与图片数量
- `GET /api/admin/images`：分页查询图片+标签
  - 参数：`category`、`tag`、`page`、`pageSize`
- `POST /api/admin/image/tags`：设置图片标签
  - JSON：`{"path":"web/xxx.webp","tags":["anime"],"mode":"replace|append"}`

### 统计
- `GET /api/stats`
- 统计驻留内存，定时写入 `stats.json`

---

## 静态资源与缓存

- 页面：`/public/*`
- 图片：`/images/*`
- 图片响应包含缓存头：
  - `Cache-Control: public, max-age=31536000, immutable`

这意味着随机接口可通过 302 指向静态图片，由 CDN 长缓存图片资源。

---

## 前端页面

- `/public/index.html`：首页（接口入口 + 统计 + 背景刷新）
- `/public/login.html`：Token 登录页
- `/public/upload.html`：上传页（拖拽/点选文件，点击上传按钮提交，可附加标签）
- `/public/admin.html`：后台标签管理页

---

## 目录结构

```text
One picture-API/
├─ config.go
├─ main.go
├─ tokens.json
├─ tokens.example.json
├─ stats.json
├─ tags_index.json
├─ images/
│  ├─ web/
│  └─ m/
└─ public/
   ├─ index.html
   ├─ login.html
   ├─ upload.html
   ├─ common.css
   └─ common.js
```

---

## 首次配置

不要把真实登录 Token 提交到仓库。首次运行前二选一：

```bash
cp tokens.example.json tokens.json
```

然后编辑 `tokens.json`，把示例值替换为长度 >= 32 的随机字符串；或者直接通过环境变量提供：

```bash
OPAPI_TOKENS="your-random-token" go run .
```

---

## 运行

在项目目录执行：

```bash
go run .
```

启动后访问：
- `http://localhost:8080`

---

## 公网部署建议

默认监听地址为 `127.0.0.1:8080`，推荐放在 Nginx / Caddy / Cloudflare Tunnel 等反向代理后面，再由反代负责 HTTPS、访问日志、压缩与证书续期。

如果确实需要程序直接监听所有网卡，请显式设置：

```bash
OPAPI_ADDR=":8080" go run .
```

HTTPS 部署时建议同时设置：

```bash
OPAPI_COOKIE_SECURE=true
```

如果后台页面和 API 不在同一个域名，需要配置允许的来源：

```bash
OPAPI_TRUSTED_ORIGINS="https://example.com"
```

如果服务运行在可信反向代理后，并希望登录限速使用 `X-Forwarded-For` / `X-Real-IP` 中的真实客户端 IP，可设置：

```bash
OPAPI_TRUST_PROXY=true
```

不要在未配置可信反代时开启该选项。

---

## tokens.json 示例

```json
{
  "tokens": [
    "请替换为你的高强度token"
  ]
}
```

建议使用长度 >= 32 的随机字符串，并定期轮换。

---

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OPAPI_ADDR` | `127.0.0.1:8080` | 监听地址；如需直接公网监听可设为 `:8080` |
| `OPAPI_IMAGES_DIR` | `images` | 图片根目录，下面需要有 `web/` 和 `m/` |
| `OPAPI_PUBLIC_DIR` | `public` | 前端静态页面目录 |
| `OPAPI_TOKENS_FILE` | `tokens.json` | 登录 Token 文件路径 |
| `OPAPI_TOKENS` | 空 | 额外登录 Token，支持逗号、分号、空白分隔 |
| `OPAPI_STATS_FILE` | `stats.json` | 统计文件路径，启动时会读取，运行中会定时落盘 |
| `OPAPI_TAGS_FILE` | `tags_index.json` | 标签索引文件路径 |
| `OPAPI_COOKIE_SECURE` | `false` | HTTPS 部署时建议设为 `true` |
| `OPAPI_TRUSTED_ORIGINS` | 空 | 允许跨域发起后台写请求的来源，支持逗号/分号/空白分隔 |
| `OPAPI_TRUST_PROXY` | `false` | 是否信任反向代理传入的真实客户端 IP 头 |
| `OPAPI_LOGIN_MAX_FAILS` | `8` | 登录限速窗口内最大失败次数，设为 `0` 可关闭 |
| `OPAPI_LOGIN_WINDOW` | `10m` | 登录失败计数窗口 |
| `OPAPI_LOGIN_BLOCK` | `15m` | 触发登录限速后的封禁时间 |
| `OPAPI_MAX_STORAGE_BYTES` | `0` | 图片总存储上限，单位字节；`0` 表示不限制 |
