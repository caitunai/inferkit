# Go + ONNX OCR

原生加速、CoreML、libvips/SIMD 的配置、构建与测试见 [原生加速说明](NATIVE_ACCELERATION.md)。


OCR 使用 PP-OCRv6 检测模型、识别模型与配套字典，默认部署配置为 **medium**。CLI 与现有 `serve` 共用 `services/ocr`；推理使用项目已有的 `onnxruntime_go` 和 CPU ONNX Runtime，无需 Python、Paddle、OpenCV 或远程 OCR 服务。

## 准备模型

先按 [ONNX 部署说明](GO_ONNX_SERVER.md) 安装与 Go 绑定匹配的 ONNX Runtime。本次在 Go 1.27.3、`onnxruntime_go v1.38.0`、ONNX Runtime CPU 1.31.1（macOS arm64）上验证。

在项目根目录执行：

```bash
bash scripts/prepare-ocr-models.sh models/pp-ocrv6-medium
```

脚本从 Paddle 官方源下载 medium 检测及识别 ONNX 包，从固定 PaddleOCR Git 提交下载字典，校验归档、权重和字典 SHA-256 后生成 `det.onnx`、`rec.onnx`、`dict.txt`、`SHA256SUMS`。目标目录必须不存在，失败时清理临时文件。离线部署可提供包含同一版本 `det.tar`、`rec.tar`、`dict.txt` 的目录作为第二个参数。

模型与字典来源为 [PaddleOCR](https://github.com/PaddlePaddle/PaddleOCR)，采用 [Apache-2.0 许可](https://github.com/PaddlePaddle/PaddleOCR/blob/main/LICENSE)。发布带权重的安装包时应保留其许可证及来源。

## 配置

将以下段落加入 `.app.toml`；完整可调限制见根目录 `.app.toml.example`。

```toml
[image.ocr]
enabled=true
# 生产环境建议使用绝对路径。
detectionModel="models/pp-ocrv6-medium/det.onnx"
recognitionModel="models/pp-ocrv6-medium/rec.onnx"
dictionary="models/pp-ocrv6-medium/dict.txt"
detectionSHA256="eb13b44b25bb36f89528b68720af8a61d9cf381176107f465db1757b65d086e1"
recognitionSHA256="9c09abf0957f7968c7586464b7397b84ad2387a0497a351af40e9acc71b673ba"
dictionarySHA256="b5f2bfe2bdd9448429e3e82b51c789775d9b42f2403d082b00662eb77e401c5d"
library="" # 自动寻找本机运行库，也可以指定绝对路径。
# CPU 线程、图片并发和请求容量自动分配，无需手工配置。
timeout="60s"
```

`enabled` 仅控制现有 `serve` 中的 OCR API；CLI `ocr` 直接加载配置中的模型。CLI 本身不需要 Redis 或数据库；现有 `serve` 保持其 Redis、队列和可选数据库的启动流程。未启用 OCR 时不会加载 OCR 模型或注册 OCR 路由。

部署可通过 Viper 配置或环境变量替换模型，例如 `IMAGE_OCR_DETECTIONMODEL`、`IMAGE_OCR_RECOGNITIONMODEL`、`IMAGE_OCR_DICTIONARY`、`IMAGE_OCR_ENABLED`。路径、校验值、资源限制在启动时读取，更换后重启进程。

可配置兼容的 PP-OCRv6 small/tiny 导出模型，无需修改代码。准备脚本只下载已验证的 medium。切换模型时必须同时更换正确的识别字典和 SHA-256 配置；tiny 使用自己的字典。只支持 float32、batch=1、单输入单输出的 DB 检测和 CTC 识别模型，检测输入 H/W 及识别输入 W 必须动态。识别高度默认 48，可通过 `recognitionHeight` 与兼容导出匹配。启动时检查模型形状、类型和静态类别数；推理时再次检查实际输出与字典是否匹配。small/tiny 尚未在本次验证中运行。

分类和 OCR 共用进程级 ONNX 环境；两者的 `library` 必须解析为同一配置路径。独立的引用计数保证关闭一个服务后另一个仍可使用运行库。

## 命令行

```bash
go run . --config .app.toml ocr /path/to/page.jpeg
go run . --config .app.toml ocr /path/to/a.avif /path/to/b.webp
go run . --config .app.toml ocr 'https://example.com/page.png'
```

每张图片输出一行 JSON；错误返回非零退出码，之前成功输出的行保持有效。结果包含 `text`、`lines`、`width`、`height`。无文字时成功返回 `text:""`、`lines:[]`。没有 `ocr serve` 子命令。

## API

通过现有服务命令启动：

```bash
go run . --config .app.toml serve
```

接口为 `POST /api/image/ocr`，沿用 `/api` 的 Bearer Token 鉴权。Token 配置及签发沿用 [现有 API 说明](GO_ONNX_SERVER.md)。

```bash
curl --fail-with-body http://localhost:8080/api/image/ocr \
  -H "Authorization: Bearer $TOKEN" \
  -F 'file=@/path/to/page.avif'

curl --fail-with-body http://localhost:8080/api/image/ocr \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/page.webp"}'
```

也支持 `application/x-www-form-urlencoded` 的 `url`。multipart 同时提供 `file` 与 `url` 时，只识别文件，不下载 URL。HTTP 不接受服务器本地文件路径。四种格式均根据真实文件内容解码，不信任扩展名或上传的 MIME 声明。

响应示例（坐标仅作结构示意）：

```json
{
  "status": 0,
  "message": "ok",
  "data": {
    "result": {
      "text": "中文 OCR",
      "lines": [{
        "text": "中文 OCR",
        "box": [{"x":10,"y":20},{"x":180,"y":20},{"x":180,"y":50},{"x":10,"y":50}],
        "confidence": 0.98
      }],
      "width": 800,
      "height": 600
    }
  }
}
```

四点顺序为左上、右上、右下、左下，坐标基于应用 EXIF/AVIF 容器方向后的图片。全文按文本区域读取顺序用换行符拼接；同一视觉行可能被模型检测为多个区域。使用旋转矩形、区域扩张、透视裁剪和竖向文字行旋转；当前没有额外加载页面方向分类、180°文字方向分类、曲面矫正或版面分析模型。多栏文档按几何位置排序。

错误状态：非法输入 `400`，请求取消/超时 `408`，字节、像素、文本数量或文字行宽度超限 `413`，容量满 `429`，图片下载失败 `502`，服务关闭 `503`，推理失败 `500`。具体格式沿用项目 `status/message/data` 响应。响应带 `Cache-Control: no-store`，不持久化图片、URL 或识别文本；日志不记录图片和识别内容。

## 资源与数据流

默认：编码图片最多 10 MiB、解码最多 2000 万像素；检测最长边 960，识别高度 48、最大宽度 2048；最多处理 1000 个连通候选区域和 1000 个文本区域。边长、候选数、文字行宽度均有硬上限；超限明确失败，不静默截断。

每个请求按“鉴权 → 有界请求准入/上传 → 获取推理槽位 → 文件/URL读取 → 四格式解码与方向处理 → BGR 检测预处理 → DB检测 → 连通区域/旋转框/扩张 → 透视采样 → BGR识别预处理 → CTC去重与去空白 → 结果”执行。阈值使用官方 medium 包的默认值：检测 0.2、框分数 0.45、扩张比例 1.4；识别最低置信度默认 0.5，均可配置。

CPU 线程、同时处理图片数、文字行工作位和准入请求数在启动时自动计算，不再读取 `image.ocr.concurrency`、`image.ocr.threads`、`image.ocr.maxRequests`。旧配置即使存在也不会固定执行容量，建议删除。OCR 结果没有缓存，因此没有历史数据增长或缓存清理任务。

整次识别受调用方 context 和配置超时约束；取消会传递到 HTTP 下载、预/后处理及原生 ONNX Run。关闭时拒绝新请求、取消已准入任务并等待结束，再释放 sessions 和环境。沿用原图片 URL 下载语义：仅 HTTP(S)、最多三次重定向、不使用代理环境变量、允许内网/TUN 地址。

## 验证

无需模型的测试覆盖 CTC、字典和校验值、模型输出形状、BGR归一化、坐标变换、资源边界、API错误响应和CLI输出。

```bash
go test ./...
golangci-lint run ./...
golangci-lint run --enable-only=dupl ./...
```

原生验证目录需要 `det.onnx`、`rec.onnx`、`dict.txt`，以及官方登机牌样例 `sample.png`：

```bash
curl -fL 'https://paddle-model-ecology.bj.bcebos.com/paddlex/imgs/demo_image/general_ocr_002.png' \
  -o /absolute/path/to/ocr-models/sample.png
OCR_TEST_DIR=/absolute/path/to/ocr-models \
  go test -race ./services/ocr ./api/handler ./api/server ./cmd
```

可选 `OCR_TEST_LIBRARY` 指定运行库；同时设置绝对路径 `IMAGE_FILTER_TEST_MODEL` 可验证分类/OCR共存。真实测试覆盖 medium 中文和英文识别、四格式输入、API鉴权、文件/URL输入、并发、原生Run终止后复用以及关闭时取消下载。

实现参考：[PaddleOCR PP-OCRv6](https://github.com/PaddlePaddle/PaddleOCR/tree/main/docs/version3.x/algorithm/PP-OCRv6)、[Go参考实现 ppocr-v6-go](https://github.com/lib-x/ppocr-v6-go)。运行时继续使用已有绑定，未引入另一套OCR运行库。

## 浏览器测试页面

启用 OCR 并启动现有 `serve` 后，打开 `/ocr`。在“访问设置”填写 API Token，可与图片检测页面共用已保存的凭证。粘贴、选择或拖入图片后会自动识别；输入完整图片 URL 并停止编辑约 600 毫秒后自动识别。可查看全文、逐行置信度以及图片中的文字框，并复制全文。“重新识别”按钮可显式重试同一来源。

本次 OCR 历史仅保留在当前页面内存中，刷新或关闭后清空。最多保留 20 条，并限制图片预览与结果的总容量；清空或淘汰记录会释放图片预览。图片、OCR文本和历史不写入 localStorage/sessionStorage；只有已有的 API Token 按原凭证策略保存。

各页面共用顶部功能导航，按服务实际启用情况显示“图片检测”“OCR识别”“识别记录”“配置中心”，当前页面会高亮。OCR 未启用时不注册 `/ocr` 页面，也不显示其导航入口。

发布时需先在 `storage/ui` 执行 `npm run build`，再构建 Go 二进制，以嵌入新页面资源。

## 自动识别与 CPU 性能

自动识别与图片检测页面共用有界调度：同一浏览器页面只发出一个请求；处理期间可以继续选择或粘贴图片、编辑链接，只保留最新来源，待当前请求完成且输入防抖结束后再提交。不同页面/API调用可以在服务自动分配的容量内并发执行。重复 URL 输入事件不重复请求，取消、切换输入方式或离开页面会清除相应待处理任务。缺少 Token 时打开访问设置，保存有效凭证后自动继续。

服务端的数据流为：

```text
启动：
  读取实际 CPU 与内存预算
  自动计算检测线程、文字行工作位、图片处理位和请求队列
  加载一份检测模型和一份识别模型
请求：
  有界准入 → 等待图片处理位 → 有界图片读取/解码
  → 获取全部 CPU 令牌 → 检测预处理、检测推理和 DB 后处理 → 释放令牌
  → 有界并行识别文字行（每行获取一个 CPU 令牌和一个工作位）
  → 等待全部文字行成功 → 按原读取顺序合并 → 返回结果
关闭：
  拒绝新请求 → 取消已准入工作 → 等待所有文字行退出 → 释放模型及共享运行库
```

模型、输入尺寸、检测阈值、CTC 解码和结果读取顺序保持不变。并行任务把结果写回各自的索引，完成顺序不会改变返回顺序；任一任务失败或请求取消，整组取消，不返回部分结果。不增加缓存、后台预计算、数据库或 Redis 操作，图片 URL 下载仍是唯一可能的外部网络步骤。

### CPU 与内存自动分配

不再需要 `concurrency=1`、`threads=4`、`maxRequests=4`。执行方案在模型启动时自动确定，并通过 `OCR automatic resource plan` 日志输出 `cpus`、`memory_bytes`、`detection_threads`、`recognition_threads`、`line_workers`、`image_concurrency`、`max_requests`，方便核对实际分配。

CPU 预算以可用逻辑 CPU 和 `GOMAXPROCS` 的较小值为基础。Go 1.27 的默认 `GOMAXPROCS` 会考虑 Linux CPU affinity 和 cgroup CPU 配额；资源探测还读取 cgroup v1/v2 的层级配额，处理一核或分数核容器，避免 Go 运行时最小并行度造成原生模型额外超配。支持统一层级、v1分离控制器、自定义挂载点、容器挂载根及父级更严格的限额。[Go 运行时说明](https://pkg.go.dev/runtime#GOMAXPROCS)

检测模型使用 CPU 预算对应的原生线程数；识别模型每次使用一个原生线程，并由多个文字行任务填满 CPU 预算。两者共享一套加权 CPU 令牌，避免“大量并发请求 × 每个请求满核线程”叠加。空闲时不开启线程忙等，不会为了显示 CPU 100% 而空转。[ONNX Runtime 线程管理](https://onnxruntime.ai/docs/performance/tune-performance/threading.html)

macOS 通过 `hw.memsize` 读取物理内存；Linux 读取 `/proc/meminfo` 和 cgroup v1/v2 的最严格内存限额，并保守考虑进程已有的 `GOMEMLIMIT`。探测失败时使用 512 MiB 的保守预算。采用内存预算的一半、扣除 512 MiB 模型/运行库预留后，分配图片和文字行工作位：图片按配置的最大像素、编码字节及检测张量估算；文字行计入原生输出、Go输出与激活预留。工作位数量至多为 CPU 数，低内存机器会自动减少。至少保留一张图片和一个文字行的处理能力；这是一套保守容量估算，不是原生内存的硬隔离或低内存部署成功保证。

请求容量由图片处理位、剩余内存和编码图片上限推导，短队列最多再容纳一个 CPU 预算数量的等待请求。超出自动容量仍返回 `429`；这是资源背压，而非固定的 4 请求限制。请求超时、字节和像素限制继续生效，避免极端输入使队列或内存无限增长。

CPU/内存额度在启动模型时采样。改变容器资源、CPU affinity 或 `GOMAXPROCS` 后，重启服务让原生线程池与容量一同重新计算。`GOMAXPROCS` 和容器 CPU quota 可作为运维层面的进程预算；不建议用它们伪装高于实际可用的核心数。

### 性能复测

本机 macOS arm64 / Apple M2 Pro、10 个逻辑 CPU、32 GiB 内存、CPU ONNX Runtime 1.31.1、官方 PP-OCRv6 medium，自动得到 10 个检测线程、单线程识别模型、10 个文字行工作位、10 个图片处理位和 20 个请求容量。不同服务器会得到不同结果。

对照基准涵盖整个请求生命周期：图片读取/解码、检测、全部文字行识别、结果组装及等待。单调用者使用含 32 个文字区域的 896×528 PNG；4 调用者每批使用两张同一 PNG 和两张小型无文字 AVIF。旧方案为单图片串行处理、检测/识别各4线程、请求上限4；新方案使用自动预算。预热后每项重复3批，不计模型加载。基准报告 P50/P95/P99、图片吞吐、进程CPU时间和Go分配；小样本的高分位仅用于本机对照，不代表生产尾延迟。

```bash
OCR_TEST_DIR=/absolute/path/to/ocr-models \
  go test ./services/ocr -run '^$' -bench '^BenchmarkOCRComparison$' -benchtime=3x -count=1 -benchmem

# 在部署机单独测试当前自动方案，增加批次数以获得更稳定的分位数：
OCR_TEST_DIR=/absolute/path/to/ocr-models \
  go test ./services/ocr -run '^$' -bench '^BenchmarkOCRLoad$' -benchtime=20x -count=1 -benchmem
```

同次不与构建/lint并行的对照结果：

| 方案 | 调用者 | P50 | P95 / P99 | 吞吐 | 平均进程CPU |
| --- | --- | --- | --- | --- | --- |
| 旧串行4线程 | 1 | 1.956s | 2.001s / 2.001s | 0.508 张/s | 337.6% |
| 自动资源与并行文字行 | 1 | 1.230s | 1.256s / 1.256s | 0.818 张/s | 650.2% |
| 旧串行4线程 | 4 | 1.997s | 4.074s / 4.074s | 0.989 张/s | 331.8% |
| 自动资源与并行文字行 | 4 | 0.012s | 2.140s / 2.140s | 1.875 张/s | 772.3% |

4调用者的P50包含两张快速完成的小型无文字图片，因此应结合P95、吞吐和单图数据看待；该结果不代表所有图片都能在12毫秒完成。单图P50降低约37%，混合负载吞吐提升约90%。这些结果只证明此机器/模型/测试集的变化，不能保证其他CPU、图片或生产负载的同等收益。

`cpu-%` 以一个逻辑CPU为100%，10核进程的理论上限为1000%；Go分配不包含ONNX原生内存，4调用者的B/op对应整批4张图片。基准的内存源不包含外部URL下载延迟。应在目标服务器按实际图片、并发和容器额度复测，避免边构建/运行lint边做性能比较。优先验证文本、区域顺序、尺寸与坐标一致，再比较吞吐和尾延迟；置信度容许 1e-5 数值误差。

### Debian 11

执行器和资源探测覆盖 Linux/amd64，不依赖新内核专有 API；使用已有 Debian 11 构建脚本与匹配的 ONNX Runtime，不新增 Python、OpenCV 或外部 OCR 服务。cgroup v1/v2 解析有可复现的文件系统测试，纯 Go 资源探测包可交叉编译验证；这不等于已在 Debian 11 真机测得性能。部署步骤沿用 [Debian 11 文档](DEBIAN11_DEPLOYMENT.md)。

在服务器上删除旧的三个执行配置，构建并重启后核对自动方案日志。若物理服务器启用了超线程或跨 NUMA 节点，更多线程也可能受内存带宽和调度影响，需用上述基准验证实际吞吐；CPU高占用本身不代表吞吐更好。
