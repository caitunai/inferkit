# Go CPU 图片过滤部署

当前分类模型使用完整 libvips 流水线，构建、版本要求及数值验收见 [当前模型预处理](LIBVIPS_MODEL_PIPELINE.md)。此前的 [SIMD 对照](IMAGE_PIPELINE_SIMD.md) 为旧流程历史记录。

原生加速、CoreML、libvips/SIMD 的配置、构建与测试见 [原生加速说明](NATIVE_ACCELERATION.md)。


本仓库实现位于 `services/imagefilter`，CLI 和 Gin API 共用输入处理、预处理、推理与结果复用逻辑。固定 batch=1，ONNX Runtime 默认使用 CPU，也支持在 macOS ARM 配置自动选择 CoreML。图片预处理使用 libvips，无需 Python、CUDA、Redis 或数据库即可运行图片过滤命令和独立图片 API。

第三方系统接入请阅读 [图片分类 API 接入文档](IMAGE_FILTER_API.md)，包含鉴权、上传及 URL 请求、响应字段、错误处理和客户端示例。

带 URL 的结果采集、数据库迁移、筛选列表与逐张人工纠错见 [图片识别结果采集与人工纠错](IMAGE_REVIEW.md)。配置了 MySQL 时默认采集，启动前需应用迁移；无数据库的推理部署可设置 `image.filter.recordResults=false`。

## 安装与配置

需要直接拉取运行环境时，使用 [Debian 13 amd64 Docker 镜像](DOCKER_DEPLOYMENT.md)：GitHub Actions 构建、测试后发布到 GHCR，并可同时发布到 Docker Hub，部署时只需挂载配置和模型目录。

使用项目 `go.mod` 指定的 Go 1.27、C 编译器、`CGO_ENABLED=1`、`-tags vips` 构建；推荐 `GOEXPERIMENT=simd` 启用标准库张量内核。libvips 版本必须与模型清单相同，本模型为 8.18.7；AVIF 需要带 dav1d 的 libheif 1.23+。Go 绑定固定为 `github.com/yalue/onnxruntime_go v1.38.0`，需部署与其 C API 兼容、匹配操作系统及 CPU 架构的原生 ONNX Runtime 动态库。本机使用 ONNX Runtime 1.31.1 完成验收。macOS Homebrew 标准安装路径会自动检测；其他安装方式在配置中显式指定库文件。Linux 默认加载 `libonnxruntime.so`，Windows 默认加载 `onnxruntime.dll`。

在 `.app.toml` 中配置（完整模板见 `/.app.toml.example`）：

```toml
port="8080"
mode="release"

[image.filter]
model="/Users/newbing/Documents/project/inferkit/tmp/artwork_fp32"
# 可选：显式指定当前平台的动态库绝对路径。
library=""
# CPU 线程、图片并发和请求容量根据机器资源自动分配。
timeout="30s"
maxBytes=10485760
maxPixels=20000000
bypassCache=false # 临时对比时设 true；同时关闭结果缓存和同图请求合并。
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
GOEXPERIMENT=simd go run -tags vips . --config .app.toml image-filter /path/to/image.png
GOEXPERIMENT=simd go run -tags vips . --config .app.toml image-filter 'https://example.com/image.webp'
GOEXPERIMENT=simd go run -tags vips . --config .app.toml image-filter /path/a.jpg /path/b.png

# 验证 deployment 包中的全部 fixtures（包含原始张量和完整图片路径）。
GOEXPERIMENT=simd go run -tags vips . --config .app.toml image-filter validate

# 独立运行图片 API，不初始化 Redis、Cache、Queue。
GOEXPERIMENT=simd go run -tags vips . --config .app.toml image-filter serve

# 现有完整服务同样注册此 API；保留其原有数据库、Redis、队列启动行为。
GOEXPERIMENT=simd go run -tags vips . --config .app.toml serve
```

CLI 在同一次命令中加载一次模型。任何输入失败都会返回非零退出码，之前成功处理的图片仍已写入标准输出；日志写入标准错误。`validate` 要求模型目录同时包含 `validation.json` 及完整 `fixtures/`，任一对照不通过时返回非零退出码并输出对照报告。

## HTTP API

### 浏览器检测工作台

先在 `storage/ui` 中执行 `npm ci`、`npm run build`，再从项目根目录启动 `GOEXPERIMENT=simd go run -tags vips . --config .app.toml image-filter serve`（或现有 `serve` 命令）。打开 `http://localhost:8080/image-filter`，在「访问设置」中填写已有 API Token。

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

- 每个服务实例只加载一个模型会话；根据可用 CPU 和内存自动分配独立输入/输出张量及 RunOptions 槽位。固定 batch=1，每个调用独占一个槽位，不共享可变输入输出缓冲区。原生算子线程数与处理位一起计算，详情见下方自动资源分配说明。
- 自动请求容量在 HTTP 读取上传内容前实施背压，也限制服务内部调用。请求体最多额外允许 64 KiB multipart 元数据；超出 1 MiB 的 multipart 解析内容可临时落盘，结束时关闭并删除。下载和上传都有限时、限量，解码前检查像素总量，单边尺寸另有 32768 像素的安全上限以约束极端长图的滤波内存。只处理第一张静态图片，不支持动画或多帧业务语义。
- 以实际内容 SHA-256 合并并发推理，文件和 URL 下载到相同字节时共用结果。成功结果以绝对 TTL 和固定 FIFO 容量保存在当前服务内存；只存哈希、标签及概率，不保存图片或来源 URL。失败不缓存，`cacheEntries=0` 可关闭完成结果复用，同时保留并发合并。首个执行者取消会使本次共享尝试失败，其他调用方可以重试。
- URL 仅支持 HTTP(S)，禁用代理环境变量，禁止 URL 凭据和片段；不限制目标 IP 网段。最多跟随三次重定向，每次均验证 URL 格式。连接超时最多 5 秒，TLS 握手最多 5 秒，整个下载（含响应体读取）受 `image.filter.timeout` 限制，默认 30 秒；响应头最多 32 KiB，响应体受 `maxBytes` 限制，缺少 Content-Length 时同样限量。文件附带的 URL 只验证语法，不访问网络。
- 预处理支持 JPEG/PNG/WebP 内的 EXIF 旋转与镜像，以及 AVIF 容器的 clap/irot/imir 变换；之后统一进行 ties-to-even 尺寸取整、Pillow 对齐的两遍 8 位 premultiplied RGBA LANCZOS、缩放后白底合成及 RGB NCHW。PNG fixtures 的逐值一致性不代表任意 JPEG 解码器、色彩配置或所有来源格式均逐像素一致，部署前仍应补充真实业务图片验收。
- 停机先停止 HTTP，再拒绝新推理、等待已接收调用，最后释放会话、张量、RunOptions 和原生运行时。请求取消会调用 ONNX Runtime Terminate，回调完成后才重置槽位。

## 临时关闭缓存进行对比

本地 `.app.toml` 已按本次测试要求设为 `image.filter.bypassCache=true`。重新构建并重启后，上传同一图片的每次请求都读取、解码、缩放并推理，不查询/写入完成结果缓存，也不合并并发的同内容请求。SHA-256 仍计算并返回，准入容量、超时、鉴权和结果采集保持原有语义。

启动日志应有 `cache_bypass=true`，每次请求应有 `cache_state=bypass`，并包含 `decode_ms`、`resize_ms`、`inference_ms`，`inference_attempt` 会递增。只有 `cacheEntries=0` 时仍会合并并发同图请求；完整对比应使用旁路开关。

如果配置中心发布了覆盖值，可通过现有 Viper 环境变量优先级明确开启：

```bash
IMAGE_FILTER_BYPASSCACHE=true ./inferkit --config .app.toml serve
```

测试结束把本地 `bypassCache` 改回 `false` 并重启，或用 `IMAGE_FILTER_BYPASSCACHE=false` 显式覆盖。配置模板和默认程序行为仍保留缓存。对比同一张原图、相同请求来源和模型，先区分首次请求，再预热 3 次并观察后续至少 20 次未命中请求的分布；不要把不同尺寸/格式图片或浏览器历史记录与服务端缓存混在一起。

## 自动资源分配

`image.filter.concurrency` 和 `image.filter.maxRequests` 不再生效。`image.filter.threads` 支持 `"auto"`、整数 0（均表示自动），或如 4 的正整数；线程数与并发容量共同预算，而非独立上限。启动时记录 `image filter automatic resource plan`，输出 `cpus`、`memory_bytes`、`threads`、`image_concurrency`、`max_requests`，以及部署模型的 `model_sha256` 和实际加载的 `onnxruntime_version`。模型指纹用于核对基准与部署资产，不是图片内容哈希。

启动时调用与 OCR 共用的 `services/resource`：CPU 取可用逻辑 CPU、`GOMAXPROCS` 和 Linux cgroup v1/v2 层级配额的较小值；macOS 读取物理内存，Linux 结合物理内存与 cgroup 内存限额，并保守考虑 `GOMEMLIMIT`。探测失败采用 512 MiB 内存预算。Linux cgroup v1/v2、自定义挂载点和父级限额沿用已有资源探测，不增加平台依赖。更改容器资源、CPU affinity 或 `GOMAXPROCS` 后需重启，重新建立原生线程池和处理位。

容量计算为：

```text
可分配内存 = max(1, 有效内存 / 2 - 512 MiB 模型及运行库预留)
编码图片预算 = max(1, 2 × maxBytes)
单处理位预算 = 48 × maxPixels + 输入张量字节数 + 64 MiB 激活及缩放预留
自动起点 = CPU provider 为 4，CoreML provider 为 2
目标算子线程 = min(CPU 数, max(自动起点, ceil(CPU 数 / 8)))
显式正整数 threads 时：目标算子线程 = min(CPU 数, threads)
调用者预算 = CPU 数 - 目标算子线程 + 1
处理位 = max(1, min(调用者预算, 可分配内存 / (单处理位预算 + 2 × 编码图片预算)))
原生算子线程 = 目标算子线程
剩余内存 = max(0, 可分配内存 - 处理位 × (单处理位预算 + 编码图片预算))
等待容量 = max(1, min(CPU 数, 剩余内存 / 编码图片预算))
请求容量 = 处理位 + 等待容量
```

除 `ceil` 外，以上除法向下取整。分类服务只有一个 ONNX 会话，配置 N 个算子线程时，它拥有 N−1 个额外共享工作线程，当前调用线程也参与计算；因此共同预算并发调用者和共享线程池，使 `处理位 + 算子线程 − 1 ≤ CPU 数`，并非每个处理位独立创建一整套线程池。CPU 自动方案以 4 线程为起点兼顾单图延迟，CoreML 保留较小 CPU 池；内存较少时减少处理位，不反向扩大显式线程数。这是一项自动启发式分配，不代表每个 CPU/模型组合的最优值，也不会在启动时运行额外校准。[ONNX 线程池说明](https://onnxruntime.ai/docs/performance/tune-performance/threading.html)

本机 10 个 CPU、32 GiB 内存，CPU provider 自动为 4 个算子线程、7 个处理位、17 个请求容量；CoreML 自动为 2 个算子线程、9 个处理位、19 个请求容量。单核仍只能使用 1 线程。至少保留一个处理位；容量是保守估计，包含部分原生工作内存预留，不是原生内存硬隔离或低内存部署成功保证。超出自动请求容量仍返回 `429`。

默认 `warmupRuns=3`，在 HTTP 启动之前有限预热，支持取消且最多 30 秒；`warmupRuns=0` 可关闭。默认 `coremlCache=true`，在系统用户缓存目录复用按模型内容和运行环境隔离的 CoreML 编译产物，与图片结果缓存无关。缓存最多保留 8 个目录，30 天未使用且没有活跃会话保护的目录在后续初始化时清理；不可用时回退无缓存启动。完整规则及回滚选项见 [部署说明](ONNX_DEPLOYMENT.md#图片检测启动与推理资源)。

缩放只借用当前空闲的调用者/张量处理位，已有请求占用的位不会被抢占；不在读取或解码期间借位。每个阶段按行分块，通过 `xutil.ErrGroup` 等待全部行完成，再进入下一阶段。缩放结束、取消或失败后先排空行任务，再归还借位，随后生成张量。多请求繁忙时自然减少行工作数，不增加一套与推理竞争的固定线程池。低于一个模型输入面积或不需缩放的图片串行准备，日志 `resize_workers` 是本次实际使用数。像素内部累加、横纵阶段顺序和每遍裁剪均保持不变。

分类模型的算子线程仅在运行期间最多短时自旋约 1000µs，并采用退避；`session.force_spinning_stop=1` 在最后一次并发 Run 返回时停止自旋，空闲时不保温。当前默认不启用动态分块。启动日志包含 `intra_spin_us` 和 `dynamic_block_base` 以便核对；OCR 保留其原有等待策略。设置依据见 [ONNX 线程管理](https://onnxruntime.ai/docs/performance/tune-performance/threading.html)。

请求路径及一致性边界为：

```text
HTTP 有界准入 → 解析有界上传 / URL
→ 服务准入 → 读取图片 → SHA-256 → 内容缓存 / 同内容请求合并
→ 等待自动处理位 → 解码、缩放及张量准备 → ONNX 推理 → 归还处理位
→ 验证概率及选择类别 → 更新成功结果缓存
→ 启用 URL 结果采集时同步保存数据库 → 返回原有响应
```

权威分类来源仍是已校验的部署模型，缓存仍是同一服务实例中的有界派生结果。没有增加数据库、Redis 或后台步骤；URL 下载和启用采集时的同步记录保持原有顺序与失败语义。`Close` 继续拒绝新调用、等待已接收请求退出后释放资源；等待槽位或原生推理时取消，均会在退出后归还处理位。自动预算按服务实例计算；OCR 与图片检测同时繁忙时会共享机器资源，需要另测混合负载。

## 请求耗时日志

图片检测每次请求额外输出一条 INFO 级别的 `image filter timing` 汇总，与原有 Gin `Request` 日志共享 `requestID`。无需新增配置，重新构建并重启后生效。CLI 同样记录检测流程计时，`status=0`；HTTP 计时包括解析、检测、同步保存和响应，各层复用同一个请求记录，不重复打印服务层汇总。OCR 暂不输出此汇总。

所有 `*_ms` 字段都显式使用毫秒，不受 zerolog 全局 duration 单位设置影响。只输出实际执行过的阶段；字段缺失表示该阶段未执行，不能当成已经测得耗时为零。

| 字段 | 测量范围与排查方向 |
| --- | --- |
| `total_ms` | 从进入图片检测 handler 到响应和上传临时文件清理完成；CLI 为服务调用范围 |
| `admission_ms` | HTTP 与服务准入操作的累计耗时，拒绝容量时 `failed_stage=admission` |
| `http_parse_ms` | 读取并解析 JSON、表单或 multipart 上传；包括接收上传、临时落盘和打开文件 |
| `source_read_ms` | 读取图片字节；URL 模式包括 URL 验证、DNS、连接、TLS、远端等待、下载和关闭响应体 |
| `hash_ms` | 取消检查及实际内容 SHA-256 计算 |
| `cache_lookup_ms` | 查询结果缓存和正在进行的同内容请求，包含缓存锁等待 |
| `cache_wait_ms` | 等待另一个同内容请求完成；只在 `cache_state=shared` 时出现 |
| `slot_wait_ms` | 等待可用张量/处理位；较高说明当前检测容量繁忙 |
| `decode_ms` | 图片解码、格式与像素校验、方向修正 |
| `preprocess_ms` | 预处理总计，不包含解码；包含下面三个子阶段 |
| `convert_ms` | 解码图像转换为工作用 NRGBA |
| `resize_ms` | 透明度预乘、两遍 Lanczos 缩放及反预乘 |
| `tensor_ms` | 白底合成、居中和 NCHW 输入张量准备 |
| `inference_ms` | ONNX 调用、动态取消协调和输出读取，不包含预处理 |
| `result_ms` | 概率校验及分类结果组装 |
| `cache_store_ms` | 更新成功结果缓存、结束同内容请求合并，包含缓存锁等待 |
| `persist_ms` | 同步结果记录调用，包含数据库连接池等待和 SQL；只在确实尝试保存时出现 |
| `http_response_ms` | 成功 JSON 响应序列化与服务端写出，不包含浏览器接收/渲染 |
| `http_cleanup_ms` | 关闭上传文件和删除 multipart 临时文件 |

辅助字段包括 `success`、`status`、`failed_stage`、`image_bytes`、`image_width`、`image_height`，以及准入时的 `active_requests` 快照、`max_requests`、`image_concurrency`、`threads`，以及本次缩放的 `resize_workers`。`image_format` 来自实际图片字节，区分 jpeg/png/webp/avif。`pixel_type` 标识实际解码像素类型（如 `rgba`、`nrgba`、`ycbcr`；方向变换包装或其他类型为 `other`），帮助判断颜色转换路径。`inference_attempt` 是当前模型会话的推理调用序号，包括验证、失败或取消的调用；1 表示首次调用，缓存命中不增加序号并显示 0。它不是 CPU/权重缓存已经变热的保证。图片尺寸是方向修正后的解码尺寸；缓存命中时未重新解码，因此尺寸字段为 0。读取失败时未取得完整图片，字节字段也可能为 0。`source_type` 区分 `reader`（上传或 CLI 文件）、`reader_with_url`（上传并附来源 URL）、`url` 和 `empty`。

`cache_state` 为 `hit`、`miss`、`shared` 或尚未到达缓存步骤的 `unreached`。`persist_state` 为 `saved`、`failed`、`skipped_no_url`、`skipped_disabled` 或 `not_applicable`；缓存命中并不跳过 URL 下载或同步记录，二者仍会分别计时。失败/取消保留已测阶段，原有 Warn/Error 日志继续记录错误原因，计时日志不再次记录错误消息。

排查先按 `requestID` 匹配汇总和 Gin 日志：如果 `source_read_ms` 高，先查图片来源/下载；`decode_ms`、`preprocess_ms` 高则看格式和实际像素；`inference_ms` 高则查模型与 CPU；`persist_ms` 高则查 MySQL；`slot_wait_ms` 或 `cache_wait_ms` 高则是等待。`convert_ms + resize_ms + tensor_ms` 是 `preprocess_ms` 的子项，不应与父项重复加总。阶段耗时不会精确加总到 `total_ms`，小量调度、参数处理和计时开销不单列。Gin 总耗时还包括进入 handler 前的鉴权等中间件和汇总日志写出；浏览器的完整耗时另外包含客户端网络及页面操作。

汇总仅保留固定阶段名、耗时、尺寸、状态和容量，不包含图片内容、内容哈希、完整 URL、路径、签名查询参数或 Token。日志在内存中按请求有界汇总，不增加逐阶段 I/O、持久化、后台任务或外部观测服务。`source_read_ms` 暂不进一步拆解 DNS/连接/远端等待，`persist_ms` 也不拆开连接池等待与数据库执行，因此应先依据大阶段定位，再针对相应依赖深入排查。

## PNG 解码与兼容性

PNG 的尺寸和色彩头继续由标准库验证，完整像素解码使用固定版本 [github.com/woozymasta/png v1.2.0](https://github.com/WoozyMasta/png)：它基于 Go 标准 PNG 解码器，采用已有的 klauspost zlib 和像素处理优化，amd64 SIMD 按 CPU 能力选择，arm64/其他架构有纯 Go 路径。不增加 C 库、WASM 或 Python；JPEG、WebP、AVIF 继续使用原有后端。像素和单边尺寸限制在分配像素前生效，仍验证 PNG 数据校验和并处理原有 EXIF 方向。共享图片输入层使 OCR 同样受益。

已与本机 Go 标准 PNG 测试语料逐字节比对，包括调色板、隔行和高位深图像，检查错误输入、CRC 和大小限制，并提供有界模糊对照。1070×680 合成 RGB PNG 的本机微基准从约 5.62ms 降为 5.00ms，分配次数从 532 降为 42；真实收益取决于 PNG 数据及 CPU，不能直接套用到其他编码格式。BSD 许可随 Docker 镜像的 notices 一并分发。

## 颜色转换与首次请求

NRGBA 工作缓冲区保持独立所有权。不透明 `image.RGBA` 可直接按行复制到同布局缓冲区；`image.YCbCr` 使用标准库面向 RGBA 的专用转换，再以同一不透明字节布局供后续处理。透明 RGBA、高位深、方向包装及其他像素类型继续使用标准转换路径。已逐字节核对透明度、不同起点/步长的子图，以及 444、422、420、440、411、410 等 YCbCr 采样方式，没有更换颜色公式或缩放算法。

在本机 1070×680 像素转换微基准中：

| 输入类型 | 标准转换 | 快路径 |
| --- | --- | --- |
| 不透明 RGBA | 3.69ms | 0.47ms |
| YCbCr 420 | 8.59ms | 2.86ms |

这只测颜色转换，不含解码、缩放或模型；源图包含透明度/方向包装时不能直接外推此收益。复测命令：`go test ./services/imagefilter -run '^$' -bench '^BenchmarkConversion$' -benchtime=1s -benchmem`。

上一轮单独核对颜色转换改动的完整请求对照保持两边均为 2 算子线程、9 处理位，使用相同的 1129×774 合成不透明 PNG、关闭完成结果缓存，预热后各测 50 批；并发内容各不相同。先执行保留的改动前测试二进制，再执行当前二进制，期间没有并行构建或 lint：

| 方案 | 调用者 | P50 | P95 / P99 | 吞吐（张/s） |
| --- | --- | --- | --- | --- |
| 标准颜色转换 | 1 | 50.28ms | 53.96 / 58.41ms | 19.66 |
| 快路径 | 1 | 48.04ms | 50.55 / 56.39ms | 20.64 |
| 标准颜色转换 | 8 | 83.37ms | 94.23 / 102.2ms | 89.00 |
| 快路径 | 8 | 78.17ms | 88.26 / 91.03ms | 95.39 |

此对照中单请求 P50 降低约 4.5%，8 并发吞吐提高约 7%。Go 分配大致相同；这不是用户原图或包含上传/数据库的接口实测。颜色转换只是总请求的一部分，不能把微基准的加速倍数当成整个接口的加速倍数。

启动后首张的模型推理可能包含首次内存分配、权重处理或线程调度成本。单张日志无法分离这些原因，应比较连续的不同图片（缓存未命中）的 `inference_attempt` 与推理分布。启动预热可以把部分首次工作移到接收请求之前，但不会减少稳定状态计算量；当前没有额外的启动预热或后台保温。不同原图即使最终模型尺寸相同，解码与缩放成本也不同，不能用两张不同图片的总耗时证明优化比例。

## 本轮处理阶段对照

本机 M2 Pro / 10 CPU / 32 GiB / ONNX Runtime 1.31.1 / `artwork_fp32`，不更换模型或像素算法：

| 独立测量项目 | 之前 | 本轮 | 说明 |
| --- | --- | --- | --- |
| 1070×680 合成 PNG 解码 | 5.62ms | 5.00ms | 标准库与固定版本快速解码器 |
| 1129×774 缩放与透明度处理 | 13.28ms | 2.91ms | 串行与 9 个行任务，空闲预算充足 |
| 预热后的原生推理 P50 | 22.11ms | 21.61ms | 停放与有界自旋，各 100 次 |
| 同上 P95 / P99 | 23.28 / 24.69ms | 22.04 / 22.81ms | 仅原生调用，不含图片处理 |

本轮完整处理路径使用同一批 1129×774 合成 PNG、同一模型/动态库和 2 算子线程/9 处理位，优化前后测试二进制顺序执行；没有同时运行构建或 lint。基线包含上一轮颜色转换优化，本轮再加入 PNG 解码、预算内并行缩放和有界线程自旋。两侧均无缓存命中、并发内容各不相同；本轮显式开启缓存旁路。单调用者预热后各测 50 次，8 调用者扩展复测 200 批（各 1600 次请求）：

| 调用者 | 方案 | P50 | P95 / P99 | 吞吐（张/s） |
| --- | --- | --- | --- | --- |
| 1 | 本轮之前 | 46.84ms | 47.57 / 47.74ms | 21.35 |
| 1 | 本轮之后 | 33.42ms | 34.35 / 34.49ms | 29.86 |
| 8 | 本轮之前 | 77.48ms | 83.13 / 86.18ms | 97.05 |
| 8 | 本轮之后 | 73.05ms | 78.64 / 85.35ms | 102.7 |

单请求 P50 下降约 29%，8 并发吞吐提高约 6%。最初 50 批并发样本中本轮 P95/P99 为 88.17/99.15ms，高于基线 83.22/85.55ms；因此扩大样本并交换执行顺序复测，得到上表，仍需用部署机持续压测确认尾延迟。8 并发进程 CPU 约从 805% 到 815%（单核为 100%）。不含模型加载、HTTP 上传、URL 网络或数据库操作。独立线程策略复测中，50ms 空闲窗口消耗进程 CPU 约 0.053ms（停放）和 0.094ms（有界自旋），没有观察到空闲持续忙等；短窗口只验证本次运行。

预处理阶段还保留 `convert_ms` 与 `tensor_ms` 两个子项，`resize_ms` 已包含在 `preprocess_ms` 内，不重复加总。原图未知时不能保证日志中的 26.6ms 解码或 18.6ms 缩放按同样比例下降；高并发下借到的行预算也会减少。完整请求以及目标 Linux 服务器的实际表现应按下面基准和实际 API 日志分别复测。

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

# 对照旧固定方案与自动方案；关闭完成结果缓存，每批使用不同图片，避免请求合并。
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" \
  go test ./services/imagefilter -run '^$' -bench '^BenchmarkFilterLoad$' -benchtime=50x -count=1 -benchmem

# 使用与实测日志同尺寸的合成 PNG 比较线程组合，报告 P50/P95/P99 和吞吐：
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" \
  go test ./services/imagefilter -run '^$' -bench '^BenchmarkFilterSizedLoad$' -benchtime=50x -count=1 -benchmem

# 单独分析预处理，排除解码与模型推理：
go test ./services/imagefilter -run '^$' -bench '^BenchmarkPrepareSized$/^all$' -benchtime=3s -benchmem \
  -cpuprofile=/tmp/inferkit-preprocess.cpu -o /tmp/inferkit-preprocess.test
go tool pprof -top /tmp/inferkit-preprocess.test /tmp/inferkit-preprocess.cpu

# 本轮解码、预算内行并行和原生线程策略的独立对照：
go test ./services/imageinput -run '^$' -bench '^BenchmarkPNGDecode$' -benchtime=1s -benchmem
go test ./services/imagefilter -run '^$' -bench '^BenchmarkParallelResize$' -benchtime=1s -benchmem
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" \
  go test ./services/imagefilter -run '^$' -bench '^BenchmarkInferencePolicies$' -benchtime=100x -benchmem
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" \
  go test ./services/imagefilter -run '^$' -bench '^BenchmarkFilterPolicies$' -benchtime=50x -benchmem

# PNG 兼容性模糊对照（默认系统测试缓存）：
go test ./services/imageinput -run '^$' -fuzz '^FuzzPNGDecoderParity$' -fuzztime=10s -parallel=2

# 前端交互、历史存储、模板样式和生产资源检查。
cd storage/ui
npm run build
```

可用 `IMAGE_FILTER_TEST_LIBRARY` 指定测试用动态库。没有指定模型目录时原生集成测试跳过，纯 Go 单元测试仍执行。并发、实际图片分布、URL 网络延迟及高分位响应时间需要在目标部署机器上测量；微基准不代表生产吞吐。

自动分配的验收目标是分类、哈希、标签顺序及预处理结果一致，概率允许 `1e-6` 数值误差；同样负载下提高并发吞吐且单请求耗时没有明显退化。如果部署机的代表性负载持续出现吞吐下降或 P95/P99 明显上升，应回退该版本并保留基准及自动方案日志，再调整策略；旧执行键不再是运行时回退开关。

以下为早期滤波与线程分配对照记录，不含后续颜色转换快路径和本轮优化。本机 macOS arm64 / Apple M2 Pro、10 个逻辑 CPU、32 GiB 内存、ONNX Runtime 1.31.1、`artwork_fp32`。CPU profile 使用与请求日志相同的 1129×774 尺寸，但使用可复现的合成不透明图片，并非用户原图。原实现滤波像素函数约占 67% 的 CPU 样本，透明度预乘约占 22%；将四通道共享一次滤波权重遍历，并跳过 alpha=255 时的无效预乘后，独立预处理基准由约 21.06ms 降至 13.95ms（约 34%）。保持逐通道整数累加顺序、22 位固定系数舍入、横向再纵向的两遍顺序及每遍字节裁剪，未更换缩放算法。标量参考、透明度全范围、格式/方向回归和模型原始 fixtures 校验结果一致。

完整请求对照先运行保留的优化前测试二进制，再运行当前测试二进制；两者使用相同 Go 工具链、动态库和图片生成逻辑，顺序执行，不与构建/lint并行。基线为前一版的 1 算子线程、10 处理位、20 请求容量；当前版本为 2 算子线程、9 处理位、19 请求容量。每项预热后测量，完成结果缓存关闭，并发图片内容不同以避免请求合并。1129×774 合成 PNG 和 320×320 部署包 PNG 分别测试单调用者与 8 调用者。测量内存源读取、摘要、等待、解码、预处理、模型和结果组装，不含模型加载、HTTP 上传、外部 URL 下载和数据库保存。

320×320 单调用者测量 200 次，其他项目测量 50 批；8 调用者每批 8 张，共 400 次请求。结果如下，所有延迟单位为毫秒：

| 图片 | 方案 | 调用者 | P50 | P95 / P99 | 吞吐（张/s） |
| --- | --- | --- | --- | --- | --- |
| 1129×774 合成 PNG | 优化前 | 1 | 74.31 | 75.53 / 76.81 | 13.43 |
| 1129×774 合成 PNG | 优化后 | 1 | 51.35 | 52.37 / 52.58 | 19.42 |
| 1129×774 合成 PNG | 优化前 | 8 | 91.10 | 98.70 / 104.2 | 80.88 |
| 1129×774 合成 PNG | 优化后 | 8 | 82.27 | 87.89 / 89.83 | 91.44 |
| 320×320 PNG | 优化前 | 1 | 41.83 | 43.34 / 44.58 | 23.68 |
| 320×320 PNG | 优化后 | 1 | 25.64 | 26.36 / 26.54 | 38.91 |
| 320×320 PNG | 优化前 | 8 | 50.31 | 57.85 / 61.73 | 137.3 |
| 320×320 PNG | 优化后 | 8 | 50.30 | 54.61 / 56.18 | 146.7 |

同尺寸大图的单请求 P50 降低约 31%，8 并发吞吐提高约 13%；小图单请求 P50 降低约 39%，8 并发吞吐提高约 7%。8 并发大图平均进程 CPU 由 731.3% 变为 796.9%，小图由 689.6% 变为 817.9%（一个逻辑 CPU 为 100%）。Go 分配没有明显下降，8 调用者的 `B/op` 对应整批图片，且不包含 ONNX 原生内存。

线程候选对照也包含 1、2、3、4 和全部 CPU 的算子线程数。该机器上满核算子线程对单图的额外收益有限，却挤占处理位、降低 8 并发吞吐，因此采用小型共享算子池。分位数仅描述本次样本；合成大图没有复现用户原图的内容、格式或完整 111.79ms 请求，不能承诺用户原图会降到表中的耗时。尚未在 Debian 13 真机测量，部署机应使用真实图片、CPU/内存额度及 OCR 混合负载复测。

原生运行时及线程参数参考：[Go 绑定说明](https://github.com/yalue/onnxruntime_go)、[ONNX Runtime CPU 线程管理](https://onnxruntime.ai/docs/performance/tune-performance/threading.html)。
