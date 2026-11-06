# libvips 完整图片处理链评估

本文是此前的原型分析记录；当前部署以 [Debian 13 Docker 文档](DOCKER_DEPLOYMENT.md) 为准，Debian 11 专项构建与 ABI 兼容检查已移除。

2026-11-05，使用 `tmp/images/test.{png,jpeg,webp,avif}` 四张原图，只读输入。结论是：**有进一步提速空间，但不宜把四种格式一律切换到 libvips。** 本轮完成本机原型测量，没有更换 API/CLI 的默认预处理，也没有修改模型、配置或正在运行的服务。

## 请求流程和验证范围

现有同步路径：

```text
HTTP/CLI 输入 → 有界读取 → 内容摘要 → 结果缓存旁路
  → 申请模型处理位
  → 格式、单边尺寸、像素数量校验
  → Go PNG/AVIF 或 libvips JPEG/WebP 完整解码
  → Go 独占像素
  → 借用空闲处理位执行相同的 Pillow RGBA Lanczos
  → 白底合成、居中、RGB/NCHW 张量
  → ONNX Runtime / CoreML → 结果
```

完整 libvips 原型：

```text
同一份本地压缩字节
  → 指定格式加载器 + sequential
  → 在 libvips 内部缩放，或 thumbnail_buffer 解码时缩小
  → 物化 320×203 RGBA → 复制输出
  → 使用现有 Go 张量和同一个模型检查结果
```

没有新增异步任务、缓存或远程服务。模型文件和原始图片是本次结果的依据；模型执行器未变。公开请求若要使用这条路径，仍需保留格式白名单、内容大小/像素/coded-frame 限制、方向只应用一次、透明度及白底合成顺序、取消和内存准入。原型仅处理这四张已知不透明的可信本地图片，不能直接作为不可信图片的解码器。

当前模型清单明确要求 `Pillow RGBA LANCZOS`、ties-to-even 尺寸、缩放后白底合成。本轮以 **320×203 尺寸一致、记录实际像素/概率差、测单请求和并发分布** 为判定条件。若要求逐像素一致，则候选均不能替换默认实现；若只看单请求提速，也不能据此接受并发吞吐或 P99 回退。

## 环境与测量方法

- M2 Pro，10 CPU，32GiB，Go 1.27.3，libvips 8.18.7，libheif 1.23.7。
- 现有稳定 ARM NEON 缩放/张量路径，未开启 `GOEXPERIMENT=simd`。用户日志中的 `go_simd` 是另一条实验路径，先前原图测试已发现其总体弱于稳定路径。
- libvips 全局操作缓存关闭，`vips_concurrency_set(1)`。该设置约束 libvips 工作线程，不保证各编解码器的内部线程数也是 1；本轮未单独锁定 dav1d 内部线程或测量峰值 RSS。
- `otool -L` 确认本机 libvips 直接链接 `/opt/homebrew/opt/mozjpeg/lib/libjpeg.62.dylib`。不能用独立 `pkg-config libjpeg` 的版本推断 libvips 实际链接的 JPEG 解码器。
- libheif 解码器描述符按优先级查询，首项为 `dav1d`。安装了某个插件并不代表它一定被选中。
- 四张输入都是 1070×680，目标都是 320×203。输出复制量由 2,910,400 字节减为 259,840 字节，约少 91%；解码器内部仍可能需要完整帧缓冲，不能据此声称总内存减少 91%。
- 每项先执行一次预热；单调用者 100 张，8 调用者 800 张。先完成 C 原型矩阵，再运行 Go 对照，没有同时进行编译或 lint。
- 原型包括实际 `write_to_memory`、输出复制、图像释放和线程局部清理；不把创建延迟计算图的时间误当成完成解码/缩放的时间。保存诊断 PNG 在计时之外。
- Go 对照为真实解码和预处理函数，复用张量缓冲、9 个处理位和原有空闲处理位租借规则。包括张量生成。C 原型截止到小尺寸 RGBA 复制，**尚不包含 Go 头部校验、张量生成、请求准入和推理**，因此下表是候选潜力评估，不能当作完整 API 的实测收益。
- C 原型每个调用者连续处理；Go 对照每批启动受管理的任务。两者调度方式有差别，不能把微小差异归因于编解码器。实际接入后仍需测完整请求。

## 单调用者结果

表内都是毫秒，写作 P50 / P95 / P99。普通 resize 使用 Lanczos3；本组图片上 gap=0 和 gap=2 的像素结果相同，耗时仍有差异。

| 格式 | 当前 Go 准备链 | vips load + resize，gap=0 | vips thumbnail |
| --- | ---: | ---: | ---: |
| PNG | 7.990 / 8.450 / 8.714 | 5.205 / 5.420 / 7.159 | 5.426 / 5.616 / 5.836 |
| JPEG | 4.142 / 4.350 / 4.407 | 3.431 / 3.606 / 3.658 | 3.586 / 3.634 / 3.694 |
| WebP | 7.820 / 8.029 / 8.092 | 7.154 / 7.503 / 7.658 | 4.194 / 4.249 / 4.286 |
| AVIF | 15.680 / 16.100 / 16.280 | 9.145 / 9.497 / 9.553 | 9.436 / 9.776 / 9.993 |

WebP 的重点是 **解码时缩小**，单纯用 vips resize 替换 Go resize 收益很小。AVIF 的主要候选收益来自原生 dav1d 解码。PNG/JPEG 单请求也有潜力，但还需看并发。这里没有把连续热测量与用户单次网页日志直接相减，也没有宣称 CoreML 推理会同步变快。

## 8 调用者结果

选取单调用者表现较好的普通 resize(gap=0) 或 thumbnail。P95/P99 为单张处理时间；吞吐只覆盖上述准备范围，不含模型。

| 格式 | 当前吞吐，张/秒 | vips 候选吞吐，张/秒 | 当前 P95 / P99 | vips P95 / P99 |
| --- | ---: | ---: | ---: | ---: |
| PNG，resize | 530.6 | 469.0 | 14.83 / 16.19ms | 19.08 / 19.94ms |
| JPEG，resize | 494.4 | 437.7 | 16.29 / 16.83ms | 21.35 / 25.90ms |
| WebP，thumbnail | 230.7 | 419.5 | 35.13 / 36.36ms | 22.78 / 25.03ms |
| AVIF，resize | 302.7 | 251.7 | 26.86 / 29.19ms | 47.66 / 54.71ms |

本机原型中只有 WebP 同时改善了单调用者和 8 调用者表现。AVIF 单调用者更快，但并发尾延迟明显退步，说明还要约束底层解码线程、内存/工作集并复测；不能从单张图推断服务器吞吐。PNG/JPEG 的完整 vips 处理链也没有成为本组并发负载的优势方案。

## 像素和模型结果

通过 Go 临时测试导出现有处理链缩放后的真实 RGBA，与原型输出逐 RGB 字节比较。四张图均不透明，这不是透明图或方向标签回归。

| 格式/候选 | RGB 最大差，0～255 | RGB 平均绝对差 | 有变化的 RGB 分量 | 最大类别概率绝对差 |
| --- | ---: | ---: | ---: | ---: |
| PNG，resize | 9 | 0.2151 | 18.86% | 0.000005026 |
| JPEG，resize | 9 | 0.2151 | 18.86% | 0.000005026 |
| WebP，resize | 9 | 0.2021 | 17.53% | 0.000006128 |
| WebP，thumbnail | 30 | 1.3987 | 35.92% | 0.000031495 |
| AVIF，resize | 9 | 0.3183 | 26.01% | 0.000000270 |

20 次现有 CLI/CoreML 检查包括每种原图两次和三种候选各一次，均成功，四种原图的重复结果一致，候选类别也均未变化。候选 PNG 只用于把已生成的 320×203 像素送入原有张量和模型，不是建议线上转码为 PNG；这组调用不用于衡量完整候选请求耗时。

原型通过 `cc -O3 -Wall -Wextra` 编译，临时 Go 对照测试/基准通过；仓库 `go test ./...`、默认和 `vips` 构建的完整 `golangci-lint` 均通过。生产 Go 源码没有因本轮评估发生改变。

这四张图实际只有一个画面的四种编码，并且分类概率都很接近 1，不能证明接近业务阈值、透明图、细小文字等样本也没有影响。WebP thumbnail 最大像素差尤其说明，解码时缩小改变了模型输入。普通 vips Lanczos 也没有做到与原模型要求逐字节一致。

## 接入建议

1. 先使用当前稳定 NEON/SSE2 构建，保留 CoreML 自动选择；开启实验 SIMD 并不会自动加速 libpng、WebP 或 dav1d。
2. 如要增加完整 vips 处理链，应做成明确可配置的预处理后端，默认维持现有精确路径，并保留旧路径用于对照和回退。后端/算法版本应进入缓存语义，避免切换后复用另一种预处理的结果。模型清单的验证也需明确区分算法，不能运行 vips 缩放却仍声称使用逐像素等价的 Pillow 预处理。
3. 优先评估 WebP 的 thumbnail 路径，但先用更多画面和阈值附近样本确认模型结果。JPEG 无需为了这组结果改装 mozjpeg；PNG 不宜仅凭单请求收益全局切换。
4. AVIF 应单独评估 dav1d 解码、线程数量和完整请求的混合并发。在接入公开请求前，必须保留现有 AV1 coded-frame/tile 尺寸限制；只检查 AVIF 容器声明的宽高不足以替代这些限制。libvips 普通 heif 加载器不能直接传入与当前应用等价的全部 per-context libheif 安全限制，新旧 libheif 的接口能力也不同。
5. 显式处理 EXIF、AVIF clap/irot/imir、ICC/高位深、透明度预乘/反预乘和白底顺序。`vips_resize` 不自动预乘 alpha；直接用于透明 RGBA 会产生另一种结果。
6. OCR 与分类的输入尺寸和几何需求不同，不应把分类用的 320 像素缩略图接入共享解码器。OCR 要分别测文字检测、裁切和识别，不能从本次分类结果推断 OCR 精度。

Debian 11 的官方 `libvips42` 为 8.10.5 系列，`libheif1` 为 1.11.0 系列，依赖 libjpeg62-turbo 和 libdav1d4；与本机原生库版本不同。未来 amd64 无 GPU 部署继续使用 ONNX Runtime CPU 和原有 CPU SIMD，不需要 CoreML。原生图片链是否更快，仍应在当前目标 Linux 构建、实际 CPU 和请求并发上验证，不能复制本机的结论或新版本 API。

## 本地复现与产物

原型和原始结果保留在忽略提交的 `tmp/vips-evaluation/`，不包含原始图片：

- `probe.c`：指定格式加载器、实际物化、复制、预热及 1/8 调用者测量。
- `reference-test.go.txt`：通过 Go overlay 临时加入现有包的对照测试及准备链基准，不改变生产源码。
- `native.csv`、`go-reference.txt`：全部延迟和吞吐结果，包括未在上表选出的 gap=2 项。
- `pixel-model-differences.csv`、`model-results.jsonl`：像素/概率差和实际模型结果。

在项目根目录使用系统默认构建缓存，不改工具缓存目录：

```bash
cc -O3 -Wall -Wextra tmp/vips-evaluation/probe.c \
  $(pkg-config --cflags --libs vips libheif) \
  -o /private/tmp/inferkit-vips-probe

# format: png/jpeg/webp/avif；mode: resize0/resize2/thumbnail。
# count 是每个调用者的张数，1 可改为 8。RGBA/PNG 是派生诊断文件。
/private/tmp/inferkit-vips-probe tmp/images/test.webp webp thumbnail \
  100 1 /private/tmp/inferkit-probe-webp-thumbnail.rgba 320
```

`reference-test.go.txt` 中导出位置固定为 `/private/tmp`。通过 overlay 临时加入现有包，不加入常规编译或测试集：

```bash
python3 - <<'PY'
import json
from pathlib import Path
root = Path.cwd()
overlay = {"Replace": {
    str(root / "services/imagefilter/vips_evaluation_probe_test.go"):
    str(root / "tmp/vips-evaluation/reference-test.go.txt")
}}
Path("/private/tmp/inferkit-vips-overlay.json").write_text(json.dumps(overlay))
PY

IMAGE_FILTER_TEST_IMAGES="$PWD/tmp/images" \
  go test -overlay /private/tmp/inferkit-vips-overlay.json -tags vips \
  ./services/imagefilter -run '^TestVipsProbeExportReference$' \
  -bench '^BenchmarkVipsProbeGoPreparation$' -benchtime 100x -count 1
```

参考：[libvips thumbnail 的解码时缩小](https://www.libvips.org/API/current/using-vipsthumbnail.html)、[resize 的滤波和 alpha 语义](https://www.libvips.org/API/current/method.Image.resize.html)、[mozjpeg 的编码目标和 libjpeg-turbo 关系](https://github.com/mozilla/mozjpeg)、[libheif 解码器插件](https://github.com/strukturag/libheif)、[Debian 11 libvips](https://packages.debian.org/bullseye/libvips42)、[Debian 11 libheif](https://packages.debian.org/bullseye/libheif1)。
