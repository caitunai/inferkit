# 图片处理 SIMD 分析与原图对照

> 本文记录此前模型及 CGO NEON/SSE2 内核的历史实验。当前已删除这些自定义 C 内核，改为可配置的 libvips 与 Go archsimd 双路径；当前构建和生产对比方法见 [图片预处理后端对比](IMAGE_PREPROCESS_BACKENDS.md)。下文历史构建命令不代表当前默认行为。

本轮使用 `tmp/images/test.png`、`test.jpeg`、`test.webp`、`test.avif`。四张图片均为 1070×680、均不透明。图片文件只读，不作为源码或模型资产提交。

## 原始接口日志

| 格式 | 编码字节 | 解码 | 缩放 | 推理 | 总计 |
| --- | ---: | ---: | ---: | ---: | ---: |
| PNG | 642604 | 20.12ms | 6.93ms | 12.74ms | 42.46ms |
| JPEG | 85852 | 6.72ms | 12.09ms | 13.42ms | 33.25ms |
| WebP | 50932 | 18.13ms | 8.08ms | 11.45ms | 38.38ms |
| AVIF | 30201 | 38.02ms | 3.56ms | 11.87ms | 54.14ms |

这些是每种格式各一次的 API 样本。文件越小不代表解码越快；AVIF 在本组文件中最小、解码耗时最高。PNG、WebP 和 AVIF 的解码是主要优化方向。四张图的缩放尺寸、滤波系数数量和不透明属性相同，现有缩放算法不按格式分支，不能仅凭这四次日志把 3.56～12.09ms 的差别认定为某种格式缩放更慢。还需用相同请求节奏看分布、设备调度和进程负载。

`resize_ms` 包含在 `preprocess_ms` 内；转换、缩放和张量处理不与预处理父项重复相加。解码、预处理、推理是不同阶段，CoreML 不负责 PNG/JPEG/WebP/AVIF 解码。

## 哪些部分适合 SIMD

| 阶段 | 适用性与现状 | 本轮处理 |
| --- | --- | --- |
| Lanczos RGBA 四通道乘加 | 相同滤波系数用于四个独立通道，适合向量整数乘加 | 新增稳定 NEON/SSE2 行内核和可选 Go 1.27 ARM64 SIMD 内核 |
| 颜色转换、像素搬运 | 部分可向量化，但应先消除重复工作 | 复用请求独占的 NRGBA；不透明 RGBA 复用同布局字节，少复制约 2.9MB/请求 |
| 透明度预乘、反预乘 | 逐像素独立，适合 SIMD；本组图片不透明，原实现已有跳过乘除的路径 | 保留算法，补充分阶段计时；不为本组样本增加另一套内核 |
| RGB/NCHW、白底合成、归一化 | 适合 SIMD，已由 NEON/SSE2 加速 | 保留现有张量实现，日志中约 0.16～0.21ms，整体收益上限较小 |
| PNG 逆滤波、RGB 展开 | 部分适合 SIMD，但各滤波器的数据依赖不同 | 保留既有解码器；本次原图 CPU profile 主要在 DEFLATE/Huffman 解压，RGB 展开其次，不能用 SIMD 一次替换整段解码 |
| AVIF/AV1 重建和像素滤波 | 已有 SIMD | 当前 gav1d 已使用 NEON，profile 观察到 `msacSymbolAdapt4NEON`、`cdefSums8NEON`；`decode_backend=go` 不等于未使用 SIMD |
| JPEG/WebP 原生解码 | 当前由 libvips 和底层编解码器执行 | 保留既有路径，不在 Go 中重复实现成熟编解码器 |
| 模型卷积、矩阵乘法等 | 由 CoreML/ONNX Runtime 的执行器管理 | 不把模型算子重新搬到 Go；CPU 子图仍由运行时处理 |
| OCR 概率阈值、归一化、双线性/透视采样、词表 argmax | 是后续可测的向量化候选 | 本轮没有改动 OCR 算法；CTC 时序去重、几何控制流等不能简单整体向量化 |
| SHA-256、结果组装、HTTP JSON | 本组日志耗时很小，哈希已有平台实现 | 不作为本轮主要目标 |

Go 1.27 的 `simd` 是可移植接口，`simd/archsimd` 提供架构内核接口；两者仍通过 `GOEXPERIMENT=simd` 启用。开启标志不会自动重写第三方 PNG/AVIF 解码器。[Go 发布说明](https://go.dev/doc/go1.27#simd)、[架构 SIMD 说明](https://go.dev/blog/archsimd)。AVIF 现有汇编能力见 [gav1d](https://github.com/gen2brain/gav1d)，PNG 既有优化见 [woozymasta/png](https://github.com/WoozyMasta/png)。

## 修改后的流程与不变量

```text
原流程：校验格式/尺寸 → 解码 → 复制到独占 NRGBA
        → 预乘 → 横向滤波 → 纵向滤波 → 反预乘 → 张量 → ONNX
新流程：校验格式/尺寸 → 解码 → 转移请求独占像素的所有权
        → 相同预乘 → 共享只读系数区 → 通过范围证明的 SIMD 两遍滤波
        → 相同反预乘 → 相同张量 → 相同 ONNX 模型
```

`decodedPixels` 只消费当前请求解码得到的图像。NRGBA 可直接使用；零起点的不透明 RGBA 与 NRGBA 字节相同，可换头复用。透明 RGBA、16 位、方向包装、非零起点等继续使用原有复制/转换。用于借用图像及参考计算的 `toRGBA` 保持复制语义。预处理会修改已移交的像素，不能把外部共享图像交给这个内部函数。

缩放仍使用原有 Lanczos、22 位定点系数、舍入、横后纵的两遍顺序及每遍字节裁剪。每组系数在窄化前证明 `rounding + 255 × sum(abs(coefficients)) <= MaxInt32`；不满足、系数为空或起点非法时回退标量实现。稳定内核每个输出行一次同步 CGO 调用，避免逐像素或逐 tap 跨边界。C 不保留 Go 指针，不分配原生内存；行任务继续由有界 `xutil.ErrGroup` 管理，取消后排空任务再归还处理位。

系数计算保持原浮点运算顺序，只把逐输出位置的两次小数组分配改为一个不可变系数区和可复用 scratch。各窗口切片限制容量，避免追加操作越过自身窗口。没有增加缓存或后台任务，结果缓存旁路与请求合并旁路语义保持不变。输入大小、AVIF coded-frame 限制、颜色、方向、标签顺序及模型文件均未改变。

一次实际 CLI 检查还确认了新计时字段：同一 PNG 第一次模型推理约 42.00ms、第二次约 4.90ms，第二次解码约 6.39ms、预处理约 2.57ms。首次执行器准备成本仍然存在；本轮没有把它隐藏到启动预热或后台保温中。这是 CLI 验证，不是 HTTP 性能承诺。

新增 `resize_backend` 和 `premultiply_ms`、`resize_horizontal_ms`、`resize_vertical_ms`、`unpremultiply_ms`。四个耗时都是 `resize_ms` 的子项。请求日志记录实际内核；不用缩放时内核字段可为空，范围检查回退时为 `go`。启动日志表示构建可选内核。`tensor_backend` 与推理 `execution_provider` 仍是两个独立概念。

## 原图性能对照

同一 M2 Pro / 10 CPU / 32GiB / Go 1.27.3 / ONNX Runtime 1.31.1 / libvips 8.18.7 / `artwork_fp32`。执行位均为 9，ORT 线程均为 2，CoreML compute units 均为 ALL。结果缓存和同图合并均旁路；每个子项先执行一次预热，随后单调用者测 100 次、8 调用者测 100 批共 800 次。优化前二进制、稳定内核、Go SIMD 实验二进制依次执行，没有与构建、lint 或模型回归并行。

以下为完整内存输入路径，包含读取、摘要、解码、预处理、模型及组装；不包含 HTTP、URL 下载、数据库和模型加载。连续基准与原始单次 API 日志不是同一负载，不能把下面的绝对时间直接当作网页接口承诺。

| 格式 | 方案 | 调用者 | P50 | P95 / P99 | 吞吐（张/秒） |
| --- | --- | ---: | ---: | ---: | ---: |
| PNG | 优化前 | 1 | 13.73ms | 14.87 / 16.06ms | 72.49 |
| PNG | 稳定 SIMD | 1 | 12.56ms | 14.87 / 17.16ms | 77.21 |
| PNG | Go SIMD 实验 | 1 | 13.04ms | 14.41 / 15.01ms | 76.30 |
| JPEG | 优化前 | 1 | 9.25ms | 10.09 / 10.54ms | 107.2 |
| JPEG | 稳定 SIMD | 1 | 7.19ms | 7.76 / 8.21ms | 137.6 |
| JPEG | Go SIMD 实验 | 1 | 8.04ms | 8.83 / 11.34ms | 121.7 |
| WebP | 优化前 | 1 | 13.41ms | 14.42 / 15.54ms | 74.21 |
| WebP | 稳定 SIMD | 1 | 11.78ms | 13.36 / 14.15ms | 83.95 |
| WebP | Go SIMD 实验 | 1 | 12.65ms | 13.36 / 13.68ms | 79.45 |
| AVIF | 优化前 | 1 | 21.12ms | 23.07 / 24.16ms | 46.47 |
| AVIF | 稳定 SIMD | 1 | 20.17ms | 22.27 / 22.76ms | 49.02 |
| AVIF | Go SIMD 实验 | 1 | 20.65ms | 23.92 / 24.69ms | 47.48 |
| PNG | 优化前 | 8 | 35.34ms | 48.06 / 52.67ms | 169.0 |
| PNG | 稳定 SIMD | 8 | 24.64ms | 34.31 / 36.77ms | 233.6 |
| PNG | Go SIMD 实验 | 8 | 29.57ms | 41.37 / 43.67ms | 198.2 |
| JPEG | 优化前 | 8 | 36.32ms | 49.34 / 51.67ms | 165.2 |
| JPEG | 稳定 SIMD | 8 | 25.59ms | 35.64 / 37.96ms | 226.6 |
| JPEG | Go SIMD 实验 | 8 | 30.26ms | 42.81 / 45.13ms | 193.0 |
| WebP | 优化前 | 8 | 58.17ms | 71.43 / 73.62ms | 113.6 |
| WebP | 稳定 SIMD | 8 | 49.50ms | 62.49 / 65.29ms | 130.5 |
| WebP | Go SIMD 实验 | 8 | 54.07ms | 66.78 / 70.95ms | 121.8 |
| AVIF | 优化前 | 8 | 48.73ms | 63.92 / 79.11ms | 128.2 |
| AVIF | 稳定 SIMD | 8 | 38.74ms | 53.66 / 66.72ms | 156.0 |
| AVIF | Go SIMD 实验 | 8 | 42.30ms | 56.30 / 61.43ms | 145.4 |

首轮 PNG 单请求 P99 有一次波动，因此交换顺序扩大到各 500 次：稳定路径 P50/P95/P99 为 12.00/13.19/14.29ms，优化前为 13.72/15.19/16.06ms。该复测只针对先前未确定的尾延迟，不替换其他格式的样本。

稳定路径的 Go 分配：PNG 约 8.68→5.75MB、1419→388 次；JPEG 约 7.39→4.46MB、1267→235 次；WebP 约 7.30→4.37MB、1269→237 次；AVIF 约 16.37→13.45MB、24228→23196 次。C/设备分配不在 Go B/op 内。8 并发吞吐分别提高约 38%、37%、15%、22%；AVIF 的主要解码成本没有消失。

本机稳定 NEON 路径总体优于 Go 1.27 实验路径，因此默认不启用实验。引入新 SIMD API 本身不保证更快；实验路径供受控对照，不能从本机结果推断未来 amd64 服务器的绝对速度。

## 构建和复现

```bash
# 稳定：自动选择 NEON/SSE2；JPEG/WebP 可选 libvips。
CGO_ENABLED=1 go build -tags vips -o inferkit .

# 可选实验：ARM64 上用 Go 1.27 archsimd 替换缩放内核。
# 不替换张量内核、CoreML/ONNX，也不改变 PNG/AVIF 解码器。
GOEXPERIMENT=simd CGO_ENABLED=1 go build -tags vips -o inferkit .

# 回退自定义缩放/张量内核到 Go 标量。
CGO_ENABLED=1 go build -tags 'vips,purego' -o inferkit .

IMAGE_FILTER_TEST_IMAGES="$PWD/tmp/images" \
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" IMAGE_FILTER_TEST_PROVIDER=coreml \
  go test -tags vips ./services/imagefilter -run '^$' -bench '^BenchmarkUserPipeline$' -benchtime=100x -benchmem
IMAGE_FILTER_TEST_IMAGES="$PWD/tmp/images" \
  go test -tags vips ./services/imagefilter -run '^TestUserImagesPreserveScalarTensor$' -v
```

无 libvips 的构建可去掉 `vips` 标签，稳定 SIMD 仍可使用。`purego` 只控制相应包的内核，不代表 ONNX 或全部第三方解码器不再使用原生库/汇编。Go SIMD 实验路径当前只在 ARM64 实现；amd64 稳定路径使用 SSE2，CPU 推理由 ONNX Runtime 管理。

默认、libvips、Go SIMD 实验均通过全仓库 lint、dupl 与测试；purego 回退测试通过。四张原图逐值张量对照、负系数、上下采样、极窄/长图、透明度和 32 位溢出回退均验证；稳定和实验路径均通过真实模型、并发、取消和 race 检查。amd64 SSE2 内核已交叉编译语法检查，实际 Debian 11 性能需在服务器上测量；现有 CI 会在 Debian 11 执行内核测试。
