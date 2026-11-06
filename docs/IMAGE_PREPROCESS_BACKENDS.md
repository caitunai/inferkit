# 图片预处理后端对比

同一个进程默认同时提供 libvips 和 Go 两条图片预处理路径，不需要配置启用。CLI 用 `--preprocess libvips|go` 参数选择，API 用请求路径选择。两条路径共享同一个模型、ONNX Runtime 会话、自动处理位、算子线程及请求预算；推理执行器 `provider` 单独配置，可以使用 CoreML 或 CPU。

| 后端 | JPEG 解码 | 图片处理 | 张量转换 |
| --- | --- | --- | --- |
| `libvips` | macOS mozjpeg / Debian libjpeg-turbo，保留自动 shrink-on-load | 训练配套的 thumbnail、sRGB、透明度合成、居中补白 | Go archsimd，未启用或不支持时用 Go 标量 |
| `go` | Go 标准库 `image/jpeg`，完整尺寸解码，绕过 libvips | 原有 Go Lanczos：预乘、横向与纵向两遍、反预乘、白底及居中补白 | Go archsimd，未启用或不支持时用 Go 标量 |

两条路径均支持 JPEG、PNG、WebP、AVIF。Go 路径使用已有的优化 Go PNG、Go WebP、gav1d AVIF 解码器。gav1d 是 dav1d 的 Go 移植，保留内置汇编，不调用 libheif/dav1d 动态库；`decode_backend=go` 不表示没有 SIMD。OCR 的图片处理策略保持独立。

自定义的 CGO NEON/SSE2 缩放和张量内核已删除。Go SIMD 支持 arm64 与 amd64；amd64 在调用前检查 AVX，没有 AVX 时退回标量，不要求 AVX2/AVX512。`purego` 标签只切换这些 Go 图片内核，不会关闭 libvips、ONNX Runtime 或第三方解码器内部的汇编。

## 构建与调用

构建一个带有两条路径的二进制，使用同一份模型和推理执行器运行对比：

```bash
GOEXPERIMENT=simd CGO_ENABLED=1 go build -tags vips -o inferkit .
```

```toml
[image.filter]
model="/absolute/path/artwork_fp32"
provider="auto"
coremlComputeUnits="ALL"
bypassCache=true
recordResults=false # 测试时可关闭业务数据采集
```

```bash
./inferkit --config /path/to/app.toml image-filter --preprocess libvips /path/to/test.jpeg
./inferkit --config /path/to/app.toml image-filter --preprocess go /path/to/test.jpeg
# 省略 --preprocess 时仍使用 libvips。
./inferkit --config /path/to/app.toml image-filter /path/to/test.jpeg
# 验收同样支持选择路径。
./inferkit --config /path/to/app.toml image-filter validate --preprocess go
# 一次启动，两个 API 同时可用；serve 不需要选择后端。
./inferkit --config /path/to/app.toml image-filter serve
```

| API 路径 | 后端 |
| --- | --- |
| `POST /api/image/filter` | libvips，保留原有默认入口 |
| `POST /api/image/filter/libvips` | 显式 libvips |
| `POST /api/image/filter/go` | Go 解码及 Lanczos，按构建与 CPU 能力使用 archsimd |

所有路径均使用相同的 Bearer Token、文件/URL 请求格式和 JSON 响应。两个显式入口及原有入口共享 HTTP 上传/解析容量，不会通过换路径绕过容量限制。

浏览器 `/image-filter` 测试页也提供“检测路径”选择器，覆盖三个入口并显示对应 API URL。选择图片或输入完整 URL 后自动检测；切换路径会自动重新检测当前图片，检测期间锁定选择器。结果及会话历史分别保存实际调用路径，刷新后恢复最近一次使用的路径，已有旧历史按原默认入口显示。

```bash
curl --fail-with-body "$BASE_URL/api/image/filter/go" \
  -H "Authorization: Bearer $API_TOKEN" -F 'file=@/path/to/test.jpeg'
curl --fail-with-body "$BASE_URL/api/image/filter/libvips" \
  -H "Authorization: Bearer $API_TOKEN" -F 'file=@/path/to/test.jpeg'
```

不设置 `GOEXPERIMENT=simd`，或构建时增加 `purego`，会使用标量 Go 内核。当前双后端分类服务需要 CGO 和 `vips` 构建标签；启动时继续严格检查原生库及模型版本，不会因 libvips 出错而暗中切换到 Go。原 `image.filter.preprocessBackend` 配置已移除，残留配置不会改变请求的后端选择。

## 数值与缓存边界

新模型训练采用 libvips。旧 Go 路径在 JPEG 解码、尺寸舍入、滤波顺序及颜色处理中存在差异，因此显式选择 `go` 表示启用比较路径，**不能认为它满足模型的 libvips 像素契约**。当前仍加载并校验同一份模型清单；没有开放接受任意预处理元数据的入口。

2026-11-06 再次用部署包的 11 组参考输入、同一模型及 CPU 推理复核，确认当前 Go 分支没有适配新训练流程：

| 路径 | 输入张量及推理联合验收 | 输入逐值相同 | 最大输入分量误差 |
| --- | --- | --- | --- |
| libvips | 11/11 通过 | 10/11 | 约 1/255，符合已接受的跨平台舍入容差 |
| 旧 Go | 8/11 通过 | 8/11 | 约 0.945098 |

Go 通过的 8 张原图都是已处理好的 320×320 RGB PNG，未执行缩放。其余三张需要缩放的图片全部未通过输入张量验收：640×101 RGBA 横图、101×639 RGBA 竖图、4000×3000 JPEG。最大分量差换算为 8 位值分别约 241、64、8；它们是单个分量的最大差异，不代表整张图片的平均误差。这是预处理契约检查，不是模型准确率统计。

Go 的 `lanczos()` 本身已经使用半径 3 的 `sinc(x) * sinc(x/3)`，名称改成 Lanczos3 无法解决问题。它采用 Pillow 式直接两遍滤波、22 位定点系数、逐遍裁剪和整数透明度处理；libvips `thumbnail_buffer` 则会结合加载器缩小、整倍数缩小与剩余重采样，并使用自己的坐标相位、边缘、舍入及透明度规则。当前 Go 为先横后纵，libvips 的剩余缩小为先纵后横。需要适配整条处理链，不能只替换滤波函数或开启 archsimd。

原始复核输出保存在本机 `tmp/preprocess-contract-check-20261106/libvips.json` 和 `go.json`。两条路径的原始参考张量推理误差都不超过约 `1.20e-7`，支持差异来自图片预处理，而非此轮 ONNX CPU 推理设置。当前生产使用 libvips 路径，Go 路径仅供旧实现对照。

`image-filter validate` 使用实际选中的路径。Go 与 libvips 参考图片不符时仍会输出误差和失败结果，不放宽训练容差；原始张量推理校验继续执行。评估应同时观察类别、概率变化和延迟，再决定使用哪个后端。

同一进程可交替或并发调用两个后端，不需要重启。缓存及同图合并使用带后端命名空间的键，Go 与 libvips 的同一张图分别推理和缓存；缓存总容量仍由同一个 `cacheEntries` 约束。`bypassCache=true` 同时关闭两条路径的结果缓存和同图合并，以便重复提交同一张图测量。响应中的 `sha256` 始终是原始图片摘要，不带后端前缀。

URL 结果采集维持现有表结构：libvips 记录继续使用原模型摘要作为身份；Go 比较记录使用 `SHA256(model_sha256 + NUL + preprocess_profile)` 作为独立身份，随每次推理结果传递给存储层，存入原有 `model_sha256` 列。这个内部身份不进入公开 JSON 响应。两条启动日志中的 `prediction_sha256` 可对应此列，`model_sha256` 始终记录实际 ONNX 文件摘要。这样同一 URL、图片和模型的两条路径不会被唯一索引合并，已有 libvips 记录与人工标注也不会被覆盖。

## 日志及测量

启动时分别记录两个已启用后端的资源计划，它们描述的是同一个共享预算，不是分别创建一套处理位。启动日志和请求日志均输出 `preprocess_backend`、`preprocess_profile`、`tensor_backend`。启动时另输出 `model_preprocess_profile`、`preprocess_matches_model`；Go 比较路径的 profile 为 `go_pillow_lanczos_rgb_320_v1`，`preprocess_matches_model=false`。

`preprocess_ms` 在两条路径中都包含从解码到张量转换的整个图像处理阶段。libvips 细分为 `native_*_ms`；Go 细分为 `decode_ms`、`convert_ms`、`resize_ms`、`resize_horizontal_ms`、`resize_vertical_ms`、`tensor_ms`。子阶段属于父项，不应再次累加到父项。Go 的缩放工作线程仍借用自动预算中的空闲处理位，并在结束或失败后归还；缓存命中不会伪造解码、缩放和推理耗时。

同图、同模型、同推理执行器的基准提供单调用者和 8 调用者的 P50/P95/P99、吞吐及 Go 分配。以下命令测量内存输入到结果返回，不包含 HTTP 传输、URL 下载及持久化：

```bash
IMAGE_FILTER_TEST_IMAGES="$PWD/tmp/images" \
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" \
IMAGE_FILTER_TEST_PROVIDER=coreml GOEXPERIMENT=simd CGO_ENABLED=1 \
  go test -tags vips ./services/imagefilter -run '^$' \
  -bench '^BenchmarkPreprocessorBackends$' -benchtime=100x -benchmem
```

在无 GPU 的 Debian amd64 上将 `IMAGE_FILTER_TEST_PROVIDER` 改为 `cpu`。预热后顺序执行测量，避免同时运行构建、lint 或其他基准。这里提供的是生产对比入口，不预设 Go 路径比 libvips 更快；恢复默认路径只需改用 libvips 的 CLI 参数或 API URL。

## 本机验证结果

2026-11-06，在改为按请求选择后端之前完成以下测量，图片内核相同；这不是新增路由后的端到端重测。M2 Pro / 10 CPU / 32GiB / Go 1.27.3，ONNX Runtime 1.31.1、CoreML ALL、相同新模型与四张 `tmp/images/test.*` 原图。两条路径均为自动 9 处理位、2 ORT 线程，关闭缓存及同图合并；每个子项预热一张，再测 100 次单请求或 100 批 8 并发，各重复三轮。构建、lint、其他测试没有与基准并行。

下表为三轮各自统计值的中位数，非合并样本后的百分位；时延为从读取内存输入到返回结果的完整检测时延，不是单独解码时间。

| 格式 | 后端 | 单请求 P50 / P95 / P99 | 8 并发吞吐（张/秒） |
| --- | --- | --- | --- |
| JPEG | libvips | 7.45 / 8.34 / 9.39ms | 179.9 |
| JPEG | Go archsimd | 13.87 / 14.68 / 15.48ms | 186.3 |
| PNG | libvips | 9.54 / 10.87 / 12.70ms | 174.4 |
| PNG | Go archsimd | 13.29 / 14.27 / 14.94ms | 194.2 |
| WebP | libvips | 7.91 / 8.88 / 9.73ms | 177.3 |
| WebP | Go archsimd | 18.55 / 20.66 / 21.46ms | 162.2 |
| AVIF | libvips | 13.52 / 14.35 / 14.55ms | 177.6 |
| AVIF | Go archsimd | 21.45 / 23.91 / 24.41ms | 144.1 |

单请求延迟以 libvips 更低；Go 在这次 JPEG、PNG 8 并发测量中吞吐略高，不能据此推断其他图片或 CPU 上也更快。原始日志在 `tmp/backend-comparison-20261106.log`，三轮数据和汇总 CSV 在 `tmp/backend-comparison-20261106/`，这些本机产物不提交。

实际 CLI 对比六张原图（四种格式及 `test1.jpeg`、`test2.jpeg`）均保持类别一致。`test.jpeg` 最大概率差约 `7.04e-6`；多扫描 `test1.jpeg` 最大差约 `0.016744`（1.67 个百分点），说明旧预处理不能视为训练流程的等价替换。该小样本结果也不是完整准确率验收。

验证覆盖默认构建、vips＋Go SIMD、真实模型、HTTP、标量回退及相关包 race 检查。amd64 内核进行了独立交叉编译，Debian CI 增加无 AVX 回退用例；本机没有执行 Debian 服务器测量，也没有触发 CI。
