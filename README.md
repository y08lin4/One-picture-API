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
├─ main.go
├─ tokens.json
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

## 运行

在项目目录执行：

```bash
go run main.go
```

启动后访问：
- `http://localhost:8080`

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
