# Go CPU 图片过滤部署

本仓库实现位于 `services/imagefilter`，CLI 和 Gin API 共用输入处理、预处理、推理与结果复用逻辑。固定 batch=1，仅使用 ONNX Runtime 默认 CPU Provider，无需 Python、CUDA、Redis 或数据库即可运行图片过滤命令和独立图片 API。

第三方系统接入请阅读 [图片分类 API 接入文档](IMAGE_FILTER_API.md)，包含鉴权、上传及 URL 请求、响应字段、错误处理和客户端示例。

带 URL 的结果采集、数据库迁移、筛选列表与逐张人工纠错见 [图片识别结果采集与人工纠错](IMAGE_REVIEW.md)。配置了 MySQL 时默认采集，启动前需应用迁移；无数据库的推理部署可设置 `image.filter.recordResults=false`。

## 安装与配置

Debian 11 x86_64 直接运行二进制的构建、ONNX Runtime 固定版本下载、系统 ABI 检查及 systemd 配置见 [Debian 11 原生部署](DEBIAN11_DEPLOYMENT.md)。应在 Debian 11 上启用 CGO 构建，避免引入较新系统的 glibc 依赖。

使用项目 `go.mod` 指定的 Go 1.27、C 编译器和 `CGO_ENABLED=1` 构建。Go 绑定固定为 `github.com/yalue/onnxruntime_go v1.38.0`，需部署与其 C API 兼容、匹配操作系统及 CPU 架构的原生 ONNX Runtime 动态库。本机使用 ONNX Runtime 1.31.1 完成验收。macOS Homebrew 标准安装路径会自动检测；其他安装方式在配置中显式指定库文件。Linux 默认加载 `libonnxruntime.so`，Windows 默认加载 `onnxruntime.dll`。

在 `.app.toml` 中配置（完整模板见 `/.app.toml.example`）：

```toml
port="8080"
mode="release"

[image.filter]
model="/Users/newbing/Documents/project/inferkit/tmp/artwork_fp32"
# 可选：显式指定当前平台的动态库绝对路径。
library=""
concurrency=2
threads=1
maxRequests=8
timeout="30s"
maxBytes=10485760
maxPixels=20000000
cacheEntries=1024
cacheTTL="5m"
```

`model` 是包含 `artwork.onnx`、`deployment.json`、`labels.json` 的目录，不是单个 ONNX 文件。启动时检查模型 SHA-256、节点配置、类别顺序和预处理协议。模型不会在请求期间重新加载；更换部署包后重启进程。

API 沿用现有 `[auth.api]` 配置及 Bearer Token 验证，需一并配置有效的 `secret`（或项目现有 RSA 公钥）、`audience`、`issuers`。使用现有 API 客户端身份的 Token；`sub` 应为非数字的客户端标识，以符合当前项目的 API 身份分支。请勿为使用模型而关闭鉴权。

### 创建 API Token

新增命令直接使用 `[auth.api]` 的 `secret` 签发 HS256 Token，密钥至少需要 32 字节；audience 使用配置值，issuer 默认取 `issuers` 的第一项。命令不初始化 ONNX 模型或 Redis，标准输出仅包含 Token，便于复制到页面或用于脚本。

```bash
# 默认有效期一年（365 天），默认 subject 为 api-client。
go run . --config .app.toml api-token create

# 自定义客户端名称、有效期，或选择已配置的其他 issuer。
go run . --config .app.toml api-token create --subject image-filter-client --expires-in 24h
go run . --config .app.toml api-token create --issuer another-configured-issuer
```

Token 包含 `sub`、`iss`、`aud`、`iat`、`exp` 和随机 `jti`，可以直接通过项目现有 API 中间件验证。`--issuer` 必须属于配置的允许列表，`--subject` 不能是会进入用户登录分支的纯数字 ID，`--expires-in` 至少一秒。此命令使用 HMAC 密钥签发，RSA Token 仍可通过项目原有验证流程使用。

## 命令行

```bash
# 可传入一张或多张本地图片 / HTTP(S) 图片 URL；每张输出一行 JSON。
go run . --config .app.toml image-filter /path/to/image.png
go run . --config .app.toml image-filter 'https://example.com/image.webp'
go run . --config .app.toml image-filter /path/a.jpg /path/b.png

# 验证 deployment 包中的全部 fixtures（包含原始张量和完整图片路径）。
go run . --config .app.toml image-filter validate

# 独立运行图片 API，不初始化 Redis、Cache、Queue。
go run . --config .app.toml image-filter serve

# 现有完整服务同样注册此 API；保留其原有数据库、Redis、队列启动行为。
go run . --config .app.toml serve
```

CLI 在同一次命令中加载一次模型。任何输入失败都会返回非零退出码，之前成功处理的图片仍已写入标准输出；日志写入标准错误。`validate` 要求模型目录同时包含 `validation.json` 及完整 `fixtures/`，任一对照不通过时返回非零退出码并输出对照报告。

## HTTP API

### 浏览器检测工作台

先在 `storage/ui` 中执行 `npm ci`、`npm run build`，再从项目根目录启动 `go run . --config .app.toml image-filter serve`（或现有 `serve` 命令）。打开 `http://localhost:8080/image-filter`，在「访问设置」中填写已有 API Token。

工作台支持 Ctrl/⌘+V 粘贴剪贴板图片、拖入或选择 JPEG/WebP/AVIF/PNG 文件，以及输入公开图片 URL。粘贴或选择图片后自动检测；URL 在停止输入 600 毫秒且格式有效后自动检测，无需点击按钮。相同 URL 的重复输入事件不会重复提交；可通过「重新检测」主动重试。检测期间只保留最新的新输入，等待当前请求结束后继续检测。缺少 Token 时提示填写，保存后自动检测当前输入；可取消进行中的请求及等待任务。结果展示分类、置信度和各类别概率。历史使用响应式图片网格，包含大图预览、类别、置信度和时间；成功与失败记录均可筛选、点击回看或清空。

历史使用当前标签页的 `sessionStorage`，刷新后保留，通常随标签页会话结束清除，最多 50 条；浏览器恢复会话时可能一并恢复。仅存结果、来源、时间和最大 480 像素的压缩缩略图，不存原始图片及 API Token。单张缩略图最多 40000 个字符，复杂图片逐步缩小以控制容量；旧记录的缩略图仍可显示，重新检测可生成更清晰的预览。

Token 单独保存在本站点的 `localStorage`，点击「访问设置 → 保存」或开始检测时写入，刷新或重新打开浏览器后自动恢复。默认保存一年（365 天），若 JWT 自带的 `exp` 更早，则以该期限为准。重复使用同一 Token 不延长原保存期限；过期时自动移除，请求被服务器以 401/403 拒绝时也移除并提示重新填写。点击「访问设置 → 清除」可主动移除保存的 Token，清空检测历史不会清除 Token。页面只读取 JWT 的过期时间来约束本地保存，签名和权限验证由服务器完成。

浏览器禁用存储或容量不足时，当前检测仍可完成，页面提示保存失败。

页面仅在模型配置有效且服务初始化成功时注册；推理继续使用原来的 `POST /api/image/filter` 和鉴权规则。开发时仍可用 `ui.assetMode="vite"` 配合 `npm run dev`。

生产构建的 CSS / JS 使用 `/assets/...` 同源路径，浏览器沿用页面的协议、域名和端口。HTTPS 在反向代理处终止、代理通过 HTTP 连接 Go 服务时，资源仍通过外部 HTTPS 地址加载，无需为静态资源单独配置协议。更新 Go 代码后需重新构建并部署二进制；页面模板和资源地址生成逻辑由服务端提供。

本机已验证 `onnxruntime_go v1.38.0`、`golang.org/x/image v0.47.0` 与 ONNX Runtime 1.31.1 的组合：原始张量和图片 fixtures、实际 API、并发和取消测试均通过。URL 下载使用系统 DNS 与标准网络连接，不限制 IP 网段，可沿用本机 TUN / Fake-IP 网络配置。

检测失败时服务端记录 `image filter request failed` 日志，包含 `stage`、`status`、错误原因及可用的 `source_host` / `request_id`。DNS 错误补充解析域名和 DNS 服务器，连接错误补充远端地址，远端非 200 响应补充 `remote_status` 和内容类型。日志不记录完整图片 URL 或签名查询参数；4xx 使用 Warn，5xx 使用 Error。域名解析失败还会向页面和 API 返回 `data.code: "image_url_dns_failed"`，便于区分网络故障。

### 请求格式

接口：`POST /api/image/filter`。支持 JPEG（.jpg/.jpeg）、WebP、AVIF 和 PNG；CLI 本地文件、URL 下载、API 上传和页面共用同一解码路径。不信任扩展名或声明的 MIME 类型，实际解码后判断。

AVIF 使用 [gav1d](https://github.com/gen2brain/gav1d) 的纯 Go CPU 解码器，无需额外部署 libavif 或 WASM 运行时。先检查容器声明的尺寸，再限制实际 AV1 帧与拼接图片的像素分配；读取主图，并应用容器旋转、镜像和 clean aperture。8 位和高位深图片最终转换为模型使用的 8 位 RGB 输入，仍按统一的透明白底合成与缩放协议处理。四种格式的回归样本位于 `services/imagefilter/testdata`，正常测试无需安装图片编码工具。

```bash
# 上传图片，字段名为 file。
curl --fail-with-body http://localhost:8080/api/image/filter \
  -H "Authorization: Bearer $API_TOKEN" \
  -F 'file=@/path/to/image.png'

# 仅提供 URL：服务器下载并检测。
curl --fail-with-body http://localhost:8080/api/image/filter \
  -H "Authorization: Bearer $API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/image.webp"}'

# 同时提交：检测上传文件，URL 只标识该文件的来源，不会再次下载。
curl --fail-with-body http://localhost:8080/api/image/filter \
  -H "Authorization: Bearer $API_TOKEN" \
  -F 'file=@/path/to/image.png' \
  -F 'url=https://example.com/original.png'
```

URL 也可通过 `application/x-www-form-urlencoded` 或 multipart 的 `url` 字段传入。一次只接受一张图片和一个 URL。成功响应保持项目的 `status/message/data` 格式，分类结果位于 `data.result`：

```json
{
  "status": 0,
  "message": "ok",
  "data": {
    "result": {
      "url": "https://example.com/original.png",
      "sha256": "图片实际内容的 SHA-256",
      "label": "0_invalid",
      "labels": ["0_invalid", "1_valid"],
      "probabilities": [0.9935136, 0.0064864],
      "class_id": 0,
      "confidence": 0.9935136
    }
  }
}
```

`url` 仅在调用方提供时返回，概率及标签始终保留 `labels.json` 的顺序。取最大概率作为结果，不再执行 Softmax，不添加业务阈值；业务可按自身要求决定是否复核。

| HTTP 状态 | 含义 |
| --- | --- |
| 400 | 图片损坏、格式不支持或 URL 不合法 |
| 403 | 现有 API 身份验证未通过 |
| 408 | 请求取消或处理超时 |
| 413 | 图片字节数、像素数或请求体超过限制 |
| 429 | 已达到请求容量，调用方稍后重试 |
| 502 | URL 下载或远端 HTTP 状态失败 |
| 503 | 模型服务已开始关闭，或启用采集后结果未能保存 |
| 500 | 推理失败或输出概率不合法 |

## 资源复用与安全边界

- 每个进程实例只加载一个模型会话；`concurrency` 个独立输入/输出张量及 RunOptions 槽位可复用。固定 batch=1，每个调用独占一个槽位，不共享可变输入输出缓冲区。`threads` 控制每次运算的 CPU 线程数，默认 1；按部署 CPU 和真实流量测量后再调整，避免请求并发与算子线程相乘导致过度竞争。
- `maxRequests` 在 HTTP 读取上传内容前实施背压，也限制服务内部调用。请求体最多额外允许 64 KiB multipart 元数据；超出 1 MiB 的 multipart 解析内容可临时落盘，结束时关闭并删除。下载和上传都有限时、限量，解码前检查像素总量，单边尺寸另有 32768 像素的安全上限以约束极端长图的滤波内存。只处理第一张静态图片，不支持动画或多帧业务语义。
- 以实际内容 SHA-256 合并并发推理，文件和 URL 下载到相同字节时共用结果。成功结果以绝对 TTL 和固定 FIFO 容量保存在当前服务内存；只存哈希、标签及概率，不保存图片或来源 URL。失败不缓存，`cacheEntries=0` 可关闭完成结果复用，同时保留并发合并。首个执行者取消会使本次共享尝试失败，其他调用方可以重试。
- URL 仅支持 HTTP(S)，禁用代理环境变量，禁止 URL 凭据和片段；不限制目标 IP 网段。最多跟随三次重定向，每次均验证 URL 格式。连接超时最多 5 秒，TLS 握手最多 5 秒，整个下载（含响应体读取）受 `image.filter.timeout` 限制，默认 30 秒；响应头最多 32 KiB，响应体受 `maxBytes` 限制，缺少 Content-Length 时同样限量。文件附带的 URL 只验证语法，不访问网络。
- 预处理支持 JPEG/PNG/WebP 内的 EXIF 旋转与镜像，以及 AVIF 容器的 clap/irot/imir 变换；之后统一进行 ties-to-even 尺寸取整、Pillow 对齐的两遍 8 位 premultiplied RGBA LANCZOS、缩放后白底合成及 RGB NCHW。PNG fixtures 的逐值一致性不代表任意 JPEG 解码器、色彩配置或所有来源格式均逐像素一致，部署前仍应补充真实业务图片验收。
- 停机先停止 HTTP，再拒绝新推理、等待已接收调用，最后释放会话、张量、RunOptions 和原生运行时。请求取消会调用 ONNX Runtime Terminate，回调完成后才重置槽位。

## 验证与性能复现

```bash
go test ./...
golangci-lint run ./...
golangci-lint run --enable-only=dupl ./...

# 原生模型、完整 HTTP 鉴权上传、并发与取消测试。
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" \
  go test -race ./services/imagefilter ./api/handler ./api/server

# 分别测量未命中推理路径和相同图片复用路径，使用系统默认缓存。
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" \
  go test ./services/imagefilter -run '^$' -bench BenchmarkCheck -benchmem

# 前端交互、历史存储、模板样式和生产资源检查。
cd storage/ui
npm run build
```

可用 `IMAGE_FILTER_TEST_LIBRARY` 指定测试用动态库。没有指定模型目录时原生集成测试跳过，纯 Go 单元测试仍执行。并发、实际图片分布、URL 网络延迟及高分位响应时间需要在目标部署机器上测量；微基准不代表生产吞吐。

原生运行时及线程参数参考：[Go 绑定说明](https://github.com/yalue/onnxruntime_go)、[ONNX Runtime CPU 线程管理](https://onnxruntime.ai/docs/performance/tune-performance/threading.html)。
