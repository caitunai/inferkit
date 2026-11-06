# Go、Android 和 iOS 的 ONNX 部署

当前 Python 输入基准为 `libvips_thumbnail_rgb_320_v2`。旧 Pillow 约定已被替换；与独立 Go/libvips 服务对齐及模型迁移见 [VIPS_PREPROCESSING.md](VIPS_PREPROCESSING.md)。下文输入输出协议仍适用。

导出在训练服务器的 Python 3.12 环境执行，本地 macOS 不运行 Python。导出和概率校验使用 CPU；训练支持 RTX 3090、RTX 4090、RTX 5090 及更新的 90 级型号。部署后的 Go、Android、iOS 不需要 Python、PyTorch 或 CUDA，使用各平台的 ONNX Runtime 执行同一份模型。

## 生成部署包

将 `export_classifier.py`、`requirements.txt` 与现有的 `artwork_common.py`、`normalize_images.py` 放在服务器项目目录，在已安装训练依赖的环境执行：

```bash
python -m pip install --index-url https://pypi.org/simple -r requirements.txt
python export_classifier.py \
  --weights runs/classify/artwork_yolo26s/weights/best.pt \
  --samples dataset/val \
  --output exports/artwork_fp32
```

默认导出 FP32、固定 batch=1、固定 320×320、opset=17，不量化、不添加 NMS、不简化图。这个版本用于建立跨平台数值基线；没有实际手机性能测试前，不承诺延迟、内存占用或硬件加速效果。FP32 ONNX 可能比训练完成后的 FP16 `.pt` 文件更大，这是预期现象。

导出和 PyTorch 对照预测显式使用 `quantize=None`，表示 FP32；依赖最低版本为已在本项目服务器使用的 Ultralytics 8.4.201。旧版程序显式传入 `half=False` 时，该版本会反复提示参数弃用，但仍按 FP32 执行，不会导致导出失败。服务器历史上有一次 Pillow 基准的 10/10 样本校验通过记录，最大概率差约 `4.84e-8`；该记录不代表新版 libvips 或手机端已经通过验收。迁移到新 profile 后应重新评估和生成对照包。日志中的 OpenVINO ProTip 是 Intel 硬件优化建议，不要求更改当前 ONNX 部署格式。

导出器先把权重复制到临时目录，再调用 Ultralytics 官方导出接口，不覆盖原始 `best.pt`。输出目录必须不存在；ONNX 结构检查、CPU 加载和概率校验全部通过后，才发布完整输出目录。失败时删除临时输出，修复原因后重试。`--opset` 可按目标运行时要求调整（程序允许 13～21），仍需重新通过校验。

默认最多交替选取两类各若干张真实验证图，共 8 张，另加入透明横图、竖图和 4000×3000 JPEG 三个合成对照样本，覆盖透明合成、原生尺寸取整、留白和解码缩小，共最多 11 张。`--max-samples 32` 可增加真实样本；`--samples /data/raw_validation` 可使用原始图片目录，或指定单张图片。合成图片没有业务标签，只用于检查导出数值和预处理。

每张样本都复用当前预测程序的预处理。PyTorch 与 ONNX 输入相同的 float32 张量，校验概率均为有限数、和为 1、类别一致，并满足 `abs(a-b) <= atol + rtol*abs(b)`。默认 `atol=0.0001`、`rtol=0.001`，详细概率和最大差值写入报告。近似平局导致最大概率类别改变时也会停止，不会悄悄忽略。这个检查验证导出数值一致性，不等于重新验证业务准确率。

输出结构：

```text
exports/artwork_fp32/
├── artwork.onnx       # Go、Android、iOS 共用的模型
├── labels.json        # 输出索引对应的类别，顺序从权重读取
├── deployment.json    # 输入输出节点名、形状、预处理约定、SHA-256、opset 和 IR 版本
├── validation.json    # 逐张 PyTorch/ONNX 概率对比和导出环境版本
└── fixtures/
    ├── 000_source.png       # 原始校验图片，扩展名取决于原图
    ├── 000_normalized.png   # 完整的白底 320×320 标准结果
    ├── 000_input.f32        # 小端 float32、NCHW 的标准输入
    └── ...
```

线上推理需要 `artwork.onnx` 和对应的类别、预处理配置；`fixtures/` 与 `validation.json` 用于接入验收。模型运行不依赖原来的训练目录或验证图片路径。

## 输入输出协议

| 项目 | 约定 |
| --- | --- |
| 输入节点名 | 读取 `deployment.json` 的 `input.name`，通常是 `images` |
| 输入类型 | float32 |
| 输入形状 | `[1, 3, 320, 320]`，NCHW |
| 通道顺序 | R、G、B |
| 数值范围 | `[0,1]`，8 位像素除以 255 |
| 输出节点名 | 读取 `deployment.json` 的 `output.name`，通常是 `output0` |
| 输出形状 | `[1,2]`，float32 类别概率 |
| 类别顺序 | 按 `labels.json` 读取，不能依据业务名称自行重排 |
| 决策 | 取概率最大值的索引，再查询类别名称 |
| 后处理 | 输出已经包含 Softmax，不要再次执行 Softmax；无需 NMS 或检测框解析 |

本项目权重通常对应索引 0 为 `0_invalid`，索引 1 为 `1_valid`，以实际导出的 labels 为准。置信度就是最大类别概率；如果业务需要人工复核，可以在客户端设置门槛，当前网页的 0.8 只作为起始配置，不包含在 ONNX 内。

## 图片预处理必须一致

ONNX 只包含分类网络，**不包含图片解码、方向修正、缩放或填充**。Python 基准为 `normalize_images.py` 的 `normalize_pixels`，完整的固定调用顺序以 [libvips 约定](VIPS_PREPROCESSING.md) 为准：

1. 解码单张图片，并根据 EXIF 修正旋转和镜像方向。
2. 从原压缩字节调用 libvips `thumbnail_buffer`：`width=320`、`height=320`、`size=both`、`crop=none`、`no_rotate=false`、`linear=false`、`fail_on=error`。允许 loader 自动解码缩小；不先转换为全尺寸 RGB，不调用 thumbnail_image，不额外指定 ICC profile。
3. thumbnail 内部完成方向处理、重采样时 Alpha 预乘/反预乘、末级 Lanczos3 和尺寸取整；保留实际返回的 1～320 宽高，不做第二次 resize，也不沿用旧版 Python 的取偶数尺寸规则。
4. 转换为 sRGB uchar，再在缩放后 flatten 白底。创建白色 RGB 320×320 画布，偏移为 `(320-新宽)//2`、`(320-新高)//2`，用 background embed 填充；奇数留白的多出 1 像素在右侧或下侧。
5. 把 R、G、B 分别写入三个连续平面，float32 像素除以 float32 的 255。不要使用 BGR、NHWC、ImageNet mean/std，或再做中心裁剪。

张量的一维索引为 `(通道 * 320 + y) * 320 + x`；每张图共 307,200 个 float32，标准小端二进制文件为 1,228,800 字节。

Go 端应使用与导出配置匹配的 libvips 版本、codec 和 thumbnail_buffer 调用顺序，不再模拟 Pillow。v1 显式 resize 与 v2 解码缩小不能视为像素相同。先对照标准图，再在真实图片上评估客户端预处理造成的预测变化。合成对照样本不覆盖所有 EXIF、色彩配置和图片格式，需要补充实际业务图片验收。手机端图像库同样要以新样本为基准。

## Go 服务接入

可编译的 Go API 服务、依赖安装、上传 JPEG/WebP/PNG、图片 URL 下载和标准样本校验教程见 [GO_ONNX_SERVER.md](GO_ONNX_SERVER.md)，代码位于 [go-server/](go-server/)。

Go 可以通过 ONNX Runtime C API 的 Go 绑定加载模型，例如第三方项目 [onnxruntime_go](https://github.com/yalue/onnxruntime_go)。它不是 Microsoft 官方 Go SDK；部署时还需要对应系统及 CPU 架构的 ONNX Runtime 原生动态库，并按绑定项目要求匹配库版本和构建环境。

初始化时读取模型、labels 和真实输入输出节点名，创建并复用推理会话。每次请求提供 `[1,3,320,320]` 的 float32 输入，读取 `[1,2]` 概率。该文件固定 batch=1，多个请求可按所选绑定的并发和内存管理约定处理；不能直接塞入 batch>1 的张量。

## Android / iOS 接入

按 [ONNX Runtime Mobile 官方说明](https://onnxruntime.ai/docs/tutorials/mobile/)集成各平台 SDK：Android 使用 Java/Kotlin 接口或 C/C++ API；iOS 使用 Objective-C 接口并从 Swift 桥接，或使用 C/C++ API。

先用完整 ONNX Runtime 和 CPU 执行，确认输入输出及样本一致；后续再分别评估 Android 的 NNAPI 或 iOS 的 CoreML Execution Provider。加速器支持的算子和图划分会影响实际表现，不能仅凭导出成功判断支持全图加速。精简算子构建需要包含本模型所需算子。

目标运行时要同时支持 `deployment.json` 中的 ONNX opset 和 IR 版本，参照 [ONNX Runtime 兼容表](https://onnxruntime.ai/docs/reference/compatibility.html)。只设置 opset=17 不代表任意旧版 Mobile SDK 都可加载；遇到 IR 版本错误应更新运行时或使用匹配的导出工具环境，不要直接篡改模型版本号。

## 跨平台验收顺序

1. 直接读取 `fixtures/000_input.f32`（小端 float32）作为输入，验证 Go/手机运行时的两类概率与 `validation.json` 对应的 ONNX 概率一致。这一步绕过图片预处理。
2. 输入同一条目的 `source_file`，由目标端执行解码和预处理，对照 `normalized_file` 及 `input_file`，排查通道、方向、透明合成、取整和缩放差异。
3. 在两类真实业务图片上检查预测和误判，再测目标手机的耗时、内存和连续推理。若后续导出 FP16/INT8，应建立独立输出并重复这些检查，不能继承 FP32 的校验结论。

服务器端的导出辅助测试可以单独运行（不实际导出模型）：

```bash
python -m unittest tests.test_export_classifier -v
```

导出接口依据 [Ultralytics ONNX 官方文档](https://docs.ultralytics.com/integrations/onnx/)。本地只编写和静态审查代码，不执行 Python；ONNX 文件和数值校验报告需要按上述命令在服务器生成。
