# One-picture-API

基于 **Go 语言** 开发的轻量级随机图 API，支持 PC/手机不同接口、JSON 返回、图片上传、使用统计以及网页展示。无需数据库，直接存储图片文件，适合快速搭建个人或小型图片服务。

---

## 功能特点

- **随机图片接口**
  - `/api/web` → PC 端随机图片（直接跳转）
  - `/api/m` → 手机端随机图片（直接跳转）
  - `/api/web/json` → PC 图片 URL（JSON 返回）
  - `/api/m/json` → 手机图片 URL（JSON 返回）

- **上传图片**
  - 页面 `/public/upload.html` 可上传图片
  - 支持上传到 `/images/web`（PC）或 `/images/m`（手机）
  - 上传需要 **token 鉴权**，token 配置在 `tokens.json`

- **使用统计**
  - 每个接口统计今日调用次数 + 总调用次数
  - 统计数据存储在内存，并定期写入 `stats.json`
  - 可通过 `/api/stats` 查看统计信息

- **网页访问**
  - 首页 `/` 跳转到 `/public/index.html`
  - 首页展示随机背景壁纸（自动根据设备选择 PC 或手机接口）
  - 首页显示接口列表 + 实时使用统计 + 北京时间
  - 上传页面 `/public/upload.html` 可上传图片并显示上传结果

- **安全**
  - 静态文件使用安全处理，不暴露资源目录
  - 上传接口需要 token

---

## 项目目录结构
One-picture-API/
├─ images/ # 图片存储目录
│ ├─ web/ # PC 图片
│ └─ m/ # 手机图片
├─ public/ # 静态页面
│ ├─ index.html # 首页
│ └─ upload.html # 上传页面
├─ tokens.json # 上传鉴权 token
├─ stats.json # 使用统计数据（本地生成，可忽略上传）
└─ main.go # Go 主程序

运行方法

安装 Go 环境
在项目目录下运行：go run main.go
访问 http://localhost:8080
