# 当前模型的 libvips 预处理

图片检测的 CLI 和 API 现在统一使用 `libvips_thumbnail_rgb_320_v2`。部署包来自 `tmp/artwork_fp32`，模型 SHA-256 为 `9d28cca25c765439e35cabc2f435a678e2ae996d45bacd79e434ca7afc3abe50`。`image.filter.model` 仍可配置，更换模型后重启加载；程序校验模型摘要、输入输出、标签及预处理契约。

## 实际数据流

```text
有界读取原始 JPEG / PNG / WebP / AVIF 字节
  → 内容摘要 / 可配置的结果缓存
  → 申请自动预算中的处理位
  → 原生头部检查：实际格式、宽高、EXIF、页数；拒绝动画、多页及超限图片
  → libvips thumbnail_buffer：size=both、crop=none、自动方向、不启用 linear light
  → sRGB → uchar
  → 有 alpha 时 flatten(background=[255,255,255], max_alpha=255)
  → 再次 uchar → floor 居中 → 白色 embed 成 320×320 RGB
  → 物化一份小尺寸 RGB 缓冲区
  → Go SIMD：RGB/HWC 转 NCHW、float32(byte)/255，直接写入复用的模型张量
  → ONNX Runtime → 类别和概率
```

格式白名单沿用现有四种格式。Go 请求路径不再进行图片解码、方向变换、Lanczos、透明度合成或画布填充，不构造原分辨率 `image.Image`。原生 RGB 缓冲区在张量转换完成后释放；不再为通道拆分创建额外 libvips 图像或中间平面。旧 Go/Pillow 函数保留供已有测试和性能对照使用，生产检测不会回退到这些函数。

读取、鉴权、URL、缓存开关、缓存容量和 TTL、响应字段保持原有接口契约。没有新增后台任务或处理队列。结果缓存/同图合并可继续用 `image.filter.bypassCache=true` 旁路。

`services/imageinput` 与 `services/imagepipeline` 共用 `services/internal/vips` 原生后端。只有共享包声明 `pkg-config: vips libheif`，并统一拥有 libvips 初始化、进程级配置、原生调用和像素释放；上层包保留原有接口及可匹配的错误。这样不会再因两个 CGO 包分别声明 libvips 而产生 macOS 重复库链接警告。

## AVIF 的原生适配

标准 libvips heif 加载器无法接收本服务每次请求的 libheif 安全限制和指定的 decoder ID。因此 AVIF 使用原生适配：**libheif 显式选择 dav1d，仅解码一次，再把借用的原生像素平面交给 libvips thumbnail_image**。容器裁剪、旋转和镜像由 libheif 应用，EXIF 方向元数据交给 libvips；不在 Go 中处理像素。

这条适配保留 coded-frame、显示尺寸、内存块、总内存及序列帧数限制，避免只检查容器宽高却允许内部 AV1 帧过大。每个请求使用自己的 libheif context，codec 工作线程为 1；请求间并发受自动处理位控制。没有给每张图片另开一组满核线程。

10/12 位像素先在 libvips 内转换到与原 heifload 相同的 RGB16 范围，再进入缩略图、sRGB 和 uchar 流程。独立的 10 位带 alpha 参考图来自原训练函数，可检测把右对齐高位深数据误当成完整 16 位范围的错误。JPEG/PNG/WebP 严格从压缩字节进入 thumbnail_buffer；没有先生成原分辨率 Go 图像。

## 模型和数值验收

启动拒绝旧 Pillow 清单、不匹配的裁剪/拉伸/颜色/alpha 规则及矛盾的 training_preprocessing。原生 libvips 版本必须与模型清单的 libvips_version 一致，本包为 8.18.7。缺少 CGO、`vips` 构建标签、必要版本或 dav1d 时明确报错。

本机通过实际训练源码 `normalize_images.py:normalize_pixels`、pyvips 3.2.0、libvips 8.18.7 独立对照：

- 11 组模型 fixtures 中，10 组输入张量逐值一致。
- Ubuntu/AMD 导出的透明长图在 macOS/M2 上有 4 个 RGB 分量各差 1 个灰度级；本地原训练 Python 函数也产生完全相同的 4 处差异。
- 按已确认的跨平台容差，像素最多允许 1 个灰度级，归一化误差阈值另外包含一个 float32 舍入单位；类别和概率仍按导出文件的 atol/rtol 校验。
- validate 继续输出实际 `tensor_max_error`，新增 `tensor_exact` 和 `tensor_tolerance`，区分完全一致和容差内通过；没有把误差改写为零。
- Go SIMD 转换单独要求与标量 float32 除法逐值一致，覆盖全部字节值、三个通道、短输入及尾部；该部分不使用像素容差。

最终使用实际 CLI、CoreML 和新模型运行 `image-filter validate`，11/11 通过，10 组 `tensor_exact=true`，最大图片路径概率误差约 `5.13×10⁻⁸`。完整 Go 测试、相关包 race 检查、默认及原生/SIMD 构建的完整 lint 均通过；部署脚本测试 12 项通过，官方 ONNX 离线包集成项因未提供测试归档而跳过。

## macOS 构建与运行

原生依赖需要与模型配套；本机使用 Homebrew libvips 8.18.7、libheif 1.23.7，libvips 实际链接 mozjpeg 4.1.5。

```bash
brew install vips mozjpeg pkg-config onnxruntime
pkg-config --modversion vips libheif
# 确认 vips 版本与模型相同，并检查实际 JPEG 依赖。
otool -L "$(pkg-config --variable=libdir vips)/libvips.42.dylib"

GOEXPERIMENT=simd CGO_ENABLED=1 go build -tags vips -o inferkit .
./inferkit --config .app.toml image-filter validate
./inferkit --config .app.toml image-filter /path/to/image.webp
./inferkit --config .app.toml image-filter serve
```

如果包管理器提供的 libvips 版本已变化，应使用与模型版本一致的构建；不能只核对头文件或 `pkg-config libjpeg` 就认定运行库符合要求。启动日志的 `libvips_version`、`jpeg_library` 显示实际加载结果。不要通过覆盖全局动态库软链接切换 JPEG ABI。

```toml
[image.filter]
model="/absolute/path/artwork_fp32"
provider="auto"
coremlComputeUnits="ALL"
bypassCache=true # 性能对照时使用。
```

`auto` 在 macOS ARM 尝试 CoreML，在 Linux 使用 CPU；推理执行器不负责图片处理。模型位置、输入限制、超时和缓存仍使用现有配置。

## Debian 11 及以上

原生依赖通过 `scripts/prepare-image-linux.sh` 构建到私有前缀，不替换系统库。固定来源及 SHA-256 均在脚本中：

| 组件 | 固定版本 | 用途 |
| --- | --- | --- |
| libvips | 8.18.7 | 与训练一致的图像流水线 |
| libheif | 1.23.7 | AVIF 容器、受限解码和颜色转换 |
| dav1d | 1.5.4 | AV1 CPU 解码，启用原生汇编 |
| Highway | 1.4.0 | libvips 内部 SIMD 和 CPU 分派 |
| libjpeg-turbo | Debian 的 libjpeg62-turbo | JPEG CPU 解码 |

构建依赖：

```bash
sudo apt-get update
sudo apt-get install -y --no-install-recommends \
  build-essential ca-certificates curl git binutils cmake meson ninja-build nasm pkg-config \
  libglib2.0-dev libexpat1-dev libjpeg62-turbo-dev libpng-dev libwebp-dev \
  libexif-dev liblcms2-dev zlib1g-dev
```

在 Debian 11 x86_64 构建完整发布包：

```bash
bash scripts/build-debian11.sh
# 也可离线提供上述四个固定源码归档，摘要仍会校验：
INFERKIT_IMAGE_SOURCE_DIR=/absolute/path/native-source-archives \
  bash scripts/build-debian11.sh /srv/builds/inferkit-new
```

默认启用原生流水线及 Go SIMD。包中增加 `image/lib/`、原生依赖版本和许可说明、`runtime-packages.txt`；复制整个发布包后安装该文件列出的系统运行依赖。systemd 模板已加入 `/opt/inferkit/image/lib` 和 ONNX Runtime 库目录。手动执行时同样设置：

```bash
export LD_LIBRARY_PATH=/opt/inferkit/image/lib:/opt/inferkit/onnxruntime/lib
/opt/inferkit/inferkit --config /etc/inferkit/app.toml image-filter validate
```

程序和全部私有动态库均纳入 Debian 11 ABI 检查。Debian 11 系统自带的旧 libvips/libheif 不能直接替代这个与模型配套的包。在更新系统构建出的二进制也不能直接当作 Debian 11 ABI 产物。

Docker 默认 `WITH_VIPS=true`，同样从固定源码构建上述原生库，并安装 Debian libjpeg62-turbo 运行依赖。保留 `WITH_VIPS=false` / `INFERKIT_WITH_VIPS=0` 供不加载这个分类模型的其他功能使用；这些构建无法运行要求 libvips 的新模型。

## SIMD、资源及观测

Go 1.27 的 `simd/archsimd` 通过 `GOEXPERIMENT=simd` 启用。M2 使用 NEON；amd64 先检查 AVX 支持，再使用对应内核，不要求 AVX2/AVX512；没有 AVX 或未启用实验时，仅张量转换保留小规模标量实现。libvips、dav1d 和 ONNX Runtime 各自的原生 CPU 优化不依赖这个 Go 构建开关。

处理位、ORT 算子线程和请求容量仍自动计算。原生解码/中间缓冲按每个源像素 48 字节及固定运行余量预算，防止只看 Go heap 而忽略 C 内存。libvips 操作缓存关闭；原生像素由单一调用者释放。取消通过 C 原子标记、libvips progress 和 libheif 取消回调协作传递，释放前等待取消回调结束；不提前释放正在被原生工作使用的内存。

请求日志使用：

- `native_pipeline_ms`：从原生头部检查到实际生成 RGB 像素及原生清理，包含解码/缩放/颜色/合成/补白。
- `tensor_ms`：Go RGB→NCHW 和归一化。
- `preprocess_ms`：上述两部分及少量调用/释放开销的父项。
- `decode_backend=libvips`、`resize_backend=libvips`；启动日志另外记录 `avif_decoder=dav1d`、`jpeg_library`、实际 libvips 版本、模型摘要和真实张量后端。

不再把构造延迟执行图的时间当作单独 decode/resize 耗时，因此新路径不会输出误导性的 `decode_ms`、`resize_horizontal_ms` 等旧阶段。

旧的 [SIMD 报告](IMAGE_PIPELINE_SIMD.md) 和 [libvips 原型报告](VIPS_PIPELINE_EVALUATION.md) 是此前模型/处理链的历史记录，不是当前模型的部署协议。


## M2 Pro 完整内存请求对照

同一新模型、原图目录、CoreML、2 个 ORT 算子线程、9 个处理位、19 个请求容量。结果缓存及同图请求合并旁路，每项预热一次后单调用者 100 次、8 调用者 800 次。对照仅通过临时 Go overlay 将旧预处理接回测试二进制；旧路径是性能参考，不能作为新模型的正确运行或回退方案。

这包括读内存输入、摘要、图像处理、推理和组装；不包含 HTTP、URL 下载或模型加载。编译、lint、其他基准未与测量并行。旧路径使用此前较快的稳定 C NEON；新路径使用 libvips 与 Go SIMD。

| 格式 | 旧路径单请求 P50 / P95 / P99 | 新路径单请求 P50 / P95 / P99 | 旧→新 8 调用者吞吐，张/秒 | 旧→新 8 调用者 P99 |
| --- | ---: | ---: | ---: | ---: |
| PNG | 12.08 / 13.25 / 13.76ms | 9.19 / 10.97 / 11.83ms | 227.1 → 183.6 | 45.52 → 54.26ms |
| JPEG | 7.31 / 7.69 / 10.46ms | 6.91 / 7.07 / 7.20ms | 229.7 → 185.4 | 38.96 → 46.76ms |
| WebP | 11.92 / 13.16 / 13.74ms | 7.55 / 7.78 / 7.90ms | 134.1 → 186.8 | 64.64 → 47.52ms |
| AVIF | 19.82 / 21.63 / 22.88ms | 13.26 / 14.06 / 14.17ms | 163.9 → 179.3 | 55.20 → 49.56ms |

单请求 P50 分别降低约 24%、5%、37%、33%。8 调用者的 WebP/AVIF 吞吐提高约 39%/9%，PNG/JPEG 则下降约 19%。这是实际代价，不能把新流程描述为全部并发场景都更快；也不能为了恢复旧吞吐把新模型输入换回 Pillow。服务器发布前应测实际 CPU 上的混合格式 P95/P99，精度验收超限时拒绝发布；回滚需同时回滚匹配的模型、程序和原生库。

单请求 Go 分配分别约 5.75→1.45MB、4.46→0.21MB、4.37→0.13MB、13.45→0.07MB。C 分配不计入 Go B/op。整个四格式基准进程的峰值 RSS 为旧路径约 479MB、新路径约 395MB；它是进程观测值，不能代表单请求内存或所有设备内存。Go RGB→NCHW 微基准为标量约 123µs、Go SIMD 约 57µs，均零额外分配。

首版在 libvips 内复制画布、拆分三个 band 再拼接平面，造成额外开销；已移除。当前直接返回 RGB，与训练函数 write_to_memory 的边界相同，再由 Go SIMD 打包张量。原始表和资源统计保留于忽略提交的 `tmp/vips-full-pipeline/`。

本机没有 Linux/Docker 执行环境；amd64 Go SIMD 内核已交叉编译，更新后的 CI 已配置 Debian 11 完整原生构建及 ABI/运行测试，但本次没有触发 CI，也未在本机执行 Linux 构建。上述速度不是未来服务器或浏览器端到端延迟承诺。
