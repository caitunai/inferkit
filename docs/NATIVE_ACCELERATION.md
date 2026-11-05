# 原生图片处理与 CoreML 加速

当前分类模型已切换为完整 libvips 流水线，部署以 [当前模型说明](LIBVIPS_MODEL_PIPELINE.md) 为准。下文的 Go PNG、Pillow 缩放、C 张量内核和旧模型测量属于此前混合流程的历史说明；CoreML 的配置语义仍适用。

本轮原图分析、SIMD 缩放与 Go 1.27 实验对照见 [图片流水线 SIMD 分析](IMAGE_PIPELINE_SIMD.md)。

四种原图的 libvips 完整处理链、dav1d、解码时缩小及像素/概率差评估见 [libvips 处理链实测](VIPS_PIPELINE_EVALUATION.md)。该评估没有切换默认处理后端。

分类 API、命令行及 OCR 仍使用 Go + ONNX；没有新增端点或服务命令。加速后端在启动时确定，图片不离开本机。普通构建保留现有 CPU/Go 解码路径，可按部署机器分别启用下面的能力。

## CoreML

分类和 OCR 分别支持以下配置（可只修改分类）：

```toml
[image.filter]
provider="auto"
coremlComputeUnits="ALL"
bypassCache=true # 本轮关闭结果缓存和同图请求合并。

[image.ocr]
provider="cpu"
coremlComputeUnits="ALL"
```

`provider` 支持 `cpu`、`coreml`、`auto`；空值和配置模板默认 `cpu`。`auto` 在 macOS ARM 尝试 CoreML，初始化失败会记录原因并用全新的 CPU 会话选项重试；其他平台直接使用 CPU。显式 `coreml` 失败时停止启动，便于发现部署错误。推理过程中的错误保持原有失败语义，不在处理途中偷偷重试。

使用 ONNX Runtime CoreML V2 API、MLProgram（macOS 12+）。动态库必须包含 CoreML EP；本机 Homebrew ONNX Runtime 1.31.1 已通过验证。`coremlComputeUnits` 可选 `ALL`、`CPUAndGPU`、`CPUAndNeuralEngine`、`CPUOnly`，空值使用 `ALL`。CoreML 不支持的子图仍由 ORT CPU 执行；日志 `execution_provider=coreml` 表示已注册 CoreML，并不表示每个算子都在 GPU/ANE 上。启动日志另外记录模型 SHA-256、动态库版本、CPU 资源预算和等待策略。

GPU 低精度累加选项关闭。不同执行器仍可能有数值差异，因此 CPU 默认值保持不变；使用新模型时应对业务样本的类别、阈值和 OCR 文本复核。当前分类测试要求类别/哈希一致、最大概率差不超过 0.001；本机样例实测差异远小于此阈值。PP-OCRv6 medium 的登机牌样例在 CPU/CoreML 下均得到相同的 33 行文本。

没有启用额外的持久 CoreML 编译缓存，避免模型替换后命中旧编译产物。编译发生在会话创建阶段，首次推理还可能有框架准备成本，应与预热后的分布分开记录。图片结果缓存开关不会清除执行器内部的模型状态。

参考：[ONNX Runtime CoreML 配置](https://onnxruntime.ai/docs/execution-providers/CoreML-ExecutionProvider.html)。

## 此前的混合 libvips / Go / SIMD 路径（历史）

```bash
# macOS，已安装的依赖可直接复用。
brew install vips pkg-config onnxruntime
CGO_ENABLED=1 go build -tags vips -o inferkit .

# 环境变量优先于配置文件/配置中心；与本地 .app.toml 的 auto 配置一致。
IMAGE_FILTER_PROVIDER=auto IMAGE_FILTER_BYPASSCACHE=true \
  ./inferkit --config .app.toml serve
```

只有带 `vips` 标签且启用 CGO 的构建才链接 libvips。未加标签时无需安装 libvips，保持 Go 解码。不要把缺少系统动态库的原生构建文件复制到另一台机器后直接运行。

完整解码前继续用现有格式头校验像素数量和单边尺寸。原生代码仅调用 JPEG/WebP 的指定加载器，不自动选择 SVG/PDF 等其他加载器；不启用缩小解码、ICC 变换或自动旋转，原有方向处理仅应用一次。8 位 RGB/灰度图经原生加载得到 Go 独占的 NRGBA 缓冲区；高位深、CMYK、未支持的布局或原生失败时回到原有解码器。AVIF 保留原有 coded-frame 限制及容器方向逻辑。

PNG 保留固定版本的 Go 解码器和原有 CRC 校验。实测纯 CPU 的 PNG 并发负载中，试验性的 libvips PNG 路径吞吐从约 102.7 降到 97.9 张/秒，P95 从 78.4ms 上升到 82.2ms，因此该路径已撤回；当前原生构建也不使用 libvips 加载 PNG。JPEG 的 libjpeg-turbo 与 Go 解码器可能在 IDCT/色度上采样处产生像素差异，选择原生构建时应验证真实业务图片；不宣称 JPEG 逐像素一致。

libvips 全局操作缓存关闭，单次原生解码使用 1 个工作线程；请求间的并发由服务的自动容量控制，避免每张图片再开一组满核线程。同步 C 调用结束前物化所有像素，清理图像、原生输出和线程局部状态，不让 C 保留 Go 指针。全局库初始化只执行一次，保留到进程结束，避免分类/OCR 相互关闭共享库。原生额外缓冲区按每像素 8 字节计入分类和 OCR 的内存准入预算。

张量转换在 CGO 的 ARM64/AMD64 构建中自动使用 NEON/SSE2，包含透明度合成、RGB 分离、浮点归一化和白色填充。使用架构基线指令，不要求 AVX；算法与标量实现逐值相同。其他架构、关闭 CGO 或 `-tags purego` 时保留 Go 实现。SIMD 没有替换 Lanczos 缩放算法，也没有启用 fast-math。

启动日志的 `tensor_backend=simd`、`image_decoder=libvips` 表示构建能力。请求日志的 `decode_backend=libvips|go` 表示实际解码器，`execution_provider=coreml|cpu` 表示会话后端；原有阶段耗时和 `resize_workers` 保留。OCR 启动日志分别列出检测、识别后端。

参考：[libvips buffer 加载的生命周期](https://www.libvips.org/API/current/ctor.Image.new_from_buffer.html)、[原生像素缓冲区的所有权](https://www.libvips.org/API/current/method.Image.write_to_memory.html)。

## Debian 11 和 Docker（当前构建）

当前构建默认启用 `vips` 和 Go 标准 SIMD。`scripts/prepare-image-linux.sh` 生成固定版本的私有 libvips/libheif/dav1d/Highway，JPEG 依赖 Debian libjpeg62-turbo，不替换系统库。Debian 11 构建入口仍为 `bash scripts/build-debian11.sh`，完整依赖、运行库路径和验收步骤见 [当前模型说明](LIBVIPS_MODEL_PIPELINE.md)。Docker 的 `WITH_VIPS` 默认已改为 true。

本机没有 Docker/Linux 执行环境；Debian 11 构建与 ABI 检查已接入 CI，不把 macOS 数据当作服务器实测。

## 旧模型的历史测量

M2 Pro / macOS ARM64 / 10 CPU / 32 GiB / Go 1.27.3 / ONNX Runtime 1.31.1 / libvips 8.18.7。分类模型为 `artwork_fp32`，SHA-256 `11afd4b1e1e6e3549a89617c9298cae42e2db44ce823e6b8e3c21d867463ffc3`。最终组合只对 JPEG/WebP 启用 libvips；下面的 PNG 始终用同一 Go 解码器。模型、2 算子线程、9 处理位、图片生成逻辑和输入来源相同，结果缓存/同图请求合并均关闭，预热后顺序执行，期间没有并行编译或 lint。

1129×774 合成 PNG 的完整内存输入路径（读取、摘要、解码、准备、模型和组装，不含 HTTP、URL 下载、数据库和模型加载），单调用者各测 100 次，8 调用者各测 100 批/800 次请求：

| 方案 | 调用者 | P50 | P95 / P99 | 吞吐（张/秒） |
| --- | --- | --- | --- | --- |
| 上轮 CPU + Go PNG | 1 | 32.38ms | 33.29 / 33.48ms | 30.84 |
| 最终 CPU + Go PNG + SIMD | 1 | 32.17ms | 34.06 / 35.12ms | 30.81 |
| 最终 CoreML + Go PNG + SIMD | 1 | 15.07ms | 15.71 / 16.02ms | 66.20 |
| 上轮 CPU + Go PNG | 8 | 72.03ms | 76.65 / 78.66ms | 104.8 |
| 最终 CPU + Go PNG + SIMD | 8 | 71.98ms | 79.80 / 95.62ms | 103.8 |
| 最终 CoreML + Go PNG + SIMD | 8 | 38.37ms | 51.44 / 53.75ms | 157.0 |

CoreML 组合的单请求 P50 下降约 53%，8 并发吞吐提高约 50%；进程 CPU 从单请求约 207% / 8 并发 822%，变为约 175% / 436%（不包括 GPU/ANE 时间，单逻辑核为 100%）。CPU 方案下 PNG 总体收益很小且尾延迟有波动，不能用 SIMD 微基准推断整个 CPU 接口同比例提速。Debian 11、真实图片分布和 OCR 混合负载尚需部署后复测。

独立阶段微基准（JPEG/WebP 为同一份 1070×680 合成图片编码生成的输入，两个解码器读取完全相同的字节；每项 500ms）：

| 独立阶段 | Go 标量/原后端 | 原生路径 |
| --- | --- | --- |
| JPEG 完整解码 | 8.36ms | 4.19ms |
| WebP 完整解码 | 13.45ms | 7.44ms |
| 320×219 RGBA → 张量 | 0.440ms | 0.054ms（NEON） |

JPEG/WebP 的 Go 分配分别约为 1.13/1.22MB，原生路径约为 2.92MB，因为它直接返回完整 RGBA，而 Go JPEG/WebP 使用较紧凑的色度平面；这些数字不包括 C 分配，因此准入预算额外保留原生内存。SIMD 张量转换两侧均无分配，并通过全部通道/透明度值、不同宽度、行步长和尾部像素的逐值对照。

最终默认/vips 构建均通过完整 lint、dupl 和 `go test ./...`；纯 Go 回退测试通过。真实分类、CoreML 并发/取消/自动回退、PP-OCRv6 medium、HTTP 日志及共享运行时均通过原生 race 检查。PNG 模糊对照约 35 万次执行无失败。Debian 构建脚本的 12 项检查通过，其中官方离线归档测试因未配置归档而跳过；本机未执行 Debian 11 真机或 Docker 构建。

## 复测

使用相同模型、相同图片内容/格式、相同请求来源。关闭缓存后先检查 `cache_state=bypass` 和递增的 `inference_attempt`，预热后观察 P50/P95/P99。`resize_ms`、`convert_ms`、`tensor_ms` 属于 `preprocess_ms`，不能重复加总。大图、8 并发和 OCR 混合负载应单独测量。

```bash
golangci-lint run ./...
golangci-lint run --build-tags vips ./...
golangci-lint run --build-tags vips --enable-only=dupl ./...
go test ./...
go test -tags vips ./...
go test -tags purego ./services/imagefilter ./services/imageinput

IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" IMAGE_FILTER_TEST_COREML=1 \
  go test -tags vips ./services/imagefilter -run 'TestNativeCoreML' -v
OCR_TEST_DIR=/path/to/ocr-models OCR_TEST_COREML=1 \
  go test -tags vips ./services/ocr -run '^TestNativeCoreMLRecognition$' -v

go test -tags vips ./services/imageinput -run '^$' -bench '^BenchmarkNativeDecode$' -benchmem
go test ./services/imagefilter -run '^$' -bench '^BenchmarkTensor$' -benchmem
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" IMAGE_FILTER_TEST_COREML=1 \
  go test -tags vips ./services/imagefilter -run '^$' -bench '^BenchmarkCoreML(Sized)?$' -benchtime=100x -benchmem
```

WebP 微基准使用 `vips` 命令生成同一份输入，生成不计入解码耗时。性能基准顺序执行，避免与构建、lint、模型回归或其他 CPU 负载并行。验收要求没有越过输入/内存限制、没有泄漏或竞争，分类/文本在约定容差内，真实负载 P95/P99 没有持续退化；否则改回 `provider=cpu`，或不用 `vips` 标签重新构建。SIMD 可通过 `purego` 标签独立回退。
