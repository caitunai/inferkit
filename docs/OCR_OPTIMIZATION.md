# OCR 优化与本机对照（2026-11-06）

实现和配置见 [OCR 文档](OCR.md)。本轮补充分阶段日志、独立检测/识别 provider、有限启动预热、共享受管 CoreML 编译缓存、small 模型准备和选择、同宽自适应批量并行识别、Go SIMD CTC 及有界张量复用。

## 测量范围

Apple M2 Pro，10 CPU、32GiB，macOS 27.2；Go 1.27.3，`GOEXPERIMENT=simd`、CGO/vips，libvips 8.18.7、ONNX Runtime 1.31.1、Go 绑定 v1.38.0。

使用 `tmp/images/test.jpeg`，1070×680，SHA256 `6ad384ada1a8ce65a94d6de3bec3824687e7b5554e3fe53dcdbbfcdecc676aa6`；检测尺寸 960×608，7 个区域、7 行返回。覆盖真实 `Service.Recognize` 的图片读取、解码、预处理、两阶段推理和结果组装；不含 HTTP 上传、鉴权或外部 URL 下载。它不是最初网页测试的 803×510 PNG，因此不能直接把 891ms 当作本次输入的基线。

稳态方案各执行三个独立进程，每轮预热后记录 10 次完整请求。表中为三轮各自最近秩分位数的中位数，P95/P99 在每轮仅 10 次时都等于最大样本；不足以推断生产尾延迟。计时未与构建、lint、测试或其他基准并行。系统缓存、温度和调度仍会产生波动。

优化前使用提交 `e9037e2` 的原测试二进制与隔离测量 overlay；混合 provider 当时仅为实验。优化后的混合 provider 直接使用本次生产配置和实现。各方案不改变检测上限、阈值、识别高度或字典。

## 稳态结果

| 模型 | 检测 / 识别 | 原 P50 ms | 新 P50 ms | 新 P95 ms | 新 P99 ms |
| --- | --- | ---: | ---: | ---: | ---: |
| medium | CPU / CPU | 517.51 | 527.1 | 549.9 | 549.9 |
| medium | CoreML / CoreML | 700.15 | 620.2 | 633.9 | 633.9 |
| medium | CoreML / CPU | 351.64 | 350.6 | 364.7 | 364.7 |
| small | CPU / CPU | 139.66 | 138.2 | 141.9 | 141.9 |
| small | CoreML / CoreML | 276.56 | 283.2 | 286.5 | 286.5 |
| small | CoreML / CPU | 108.08 | 104.6 | 108.9 | 108.9 |

这张图的主要收益来自独立 provider：保留 medium，原全 CoreML 约 700ms，混合约 351ms，下降约 50%。单独批量化、SIMD 和复用并没有让所有组合的稳态延迟下降；small 全 CoreML 还略慢。CPU 行数较少时保留单行并行，较多时才同宽组批，避免减少可运行任务。

small 混合约 105ms，但与 medium 的手写字识别存在差异，例如本图 UID 的识别。没有完整人工标注业务集，不提供准确率百分比，也没有把默认 medium 切换为 small。同一模型的批量/串行验收保持文本、坐标、顺序及 `1e-5` 置信度检查；跨原生线程的已存在 FP32 差异另行验证。

## 首请求与启动

新实现、medium、CoreML 检测 / CPU 识别，同一 `test.jpeg`；每个组合创建并关闭三个服务。缓存开启的后续构造会复用系统用户缓存，不代表整机首次编译。启动列是三次平均值，首请求列是三次最近秩分位数。

| 编译缓存 | 预热周期 | 启动 ms | 首请求 P50 ms | P95 / P99 ms |
| --- | ---: | ---: | ---: | ---: |
| 关闭 | 0 | 1054 | 705.8 | 811.0 / 811.0 |
| 关闭 | 3 | 2834 | 481.1 | 483.8 / 483.8 |
| 开启 | 0 | 762.4 | 666.5 | 667.8 / 667.8 |
| 开启 | 3 | 2540 | 470.9 | 480.6 / 480.6 |

预热将部分计算提前到启动，首请求改善约 33%，同时延长启动。检测预热用 960×960，真实图为 608×960；识别预热宽 320，真实图含其他宽度，因此首次请求仍比稳态慢。没有无限预热或后台保温；预热 0–10 周期、期限 30 秒，继承命令取消。原生会话构造是同步操作，不宣称其受该预热期限中断。

## 分配与资源

用原二进制另跑 CPU 自动方案，仍为同一 JPEG、三轮各 10 次：P50 中位数 529.2ms，对照本次 527.1ms，基本持平。原 Go 分配中位数 47,372,482 B/op、582 allocs/op；新实现 13,665,136 B/op、527 allocs/op，Go 分配字节减少约 71%。原生 ONNX/CoreML 内存不计入 B/op。

复用输入、输出和 RunOptions，省去重复输入分配及已知形状输出的整块复制。slot 独占至后处理和取消回调结束，归还前清零，空闲数受模型工作位限制，每 slot 最多保留 32MiB。过大、失败和服务关闭时销毁。首次/新形状仍可能分配，模型内部的原生激活也不在该池内。

批次只合并完全相同输入宽度，最大默认 4，还受内存、模型静态 batch 及输出预算约束。SIMD 用于概率校验、CTC 融合校验/argmax，保留无效值拒绝与首个最大值语义；不改变 OCR 的透视裁剪、BGR 归一化或双线性采样。

共享 ONNX 受管会话负责缓存获取、失败时无缓存重试、活跃租约和关闭；图片分类也复用这一生命周期，避免两套缓存处理逻辑。缓存位于系统用户目录，按模型和运行环境隔离，活跃租约保护目录，沿用最多 8 个目录/30 天闲置淘汰。

## 多文字与并发对照

另外顺序运行原二进制和优化二进制的 `BenchmarkOCRLoad`，medium、CPU/CPU。单调用者为 896×528、含三十余个文字区域的登机牌 PNG；四调用者每批是两张同一 PNG 与两张小型无文字 AVIF。预热后各三轮、每轮三批，表中各统计量取三轮中位数。Go 分配在单调用者列按单图、四调用者列按整批四图计算。

| 实现 | 调用者 | P50 ms | P95 / P99 ms | 图片/s | 进程 CPU | Go MB/批 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 原实现 | 1 | 1254 | 1266 / 1266 | 0.8029 | 673.5% | 134.56 |
| 优化后 | 1 | 1219 | 1227 / 1227 | 0.8335 | 693.3% | 79.36 |
| 原实现 | 4 | 14.09 | 2201 / 2201 | 1.827 | 773.1% | 272.51 |
| 优化后 | 4 | 12.91 | 2257 / 2257 | 1.793 | 782.9% | 177.10 |

单图延迟改善约 3%，混合并发吞吐和尾延迟略有波动，没有证据证明批量方案显著提高此负载吞吐；Go 分配分别减少约 41% 和 35%。四调用者的 P50 包含快速无文字图片，不能解释为文档 13ms 完成。CPU 以单逻辑核 100% 计算，10 核上限约 1000%；CPU 占用增加本身不等于效率提升。这些小样本仅用于回归对照，仍需部署机与真实混合负载验证。

## 复现

```bash
GOEXPERIMENT=simd CGO_ENABLED=1 go build -tags vips -o inferkit .

# OCR_TEST_DIR 含 det.onnx、rec.onnx、dict.txt 和测试登机牌 sample.png。
# medium/small 分别运行；计时不与其他构建、测试并行。
OCR_TEST_DIR=/absolute/path/to/medium-model-and-fixture \
  OCR_TEST_IMAGE=/absolute/path/to/test.jpeg GOEXPERIMENT=simd CGO_ENABLED=1 \
  go test -tags vips ./services/ocr -run '^$' -bench '^BenchmarkOCRPipeline$' \
  -benchtime=10x -count=3 -benchmem

OCR_TEST_DIR=/absolute/path/to/medium-model-and-fixture \
  OCR_TEST_IMAGE=/absolute/path/to/test.jpeg OCR_TEST_COREML=1 \
  GOEXPERIMENT=simd CGO_ENABLED=1 go test -tags vips ./services/ocr \
  -run '^$' -bench '^BenchmarkOCRStartup$' -benchtime=3x -count=1

bash scripts/prepare-ocr-models.sh --size small models/pp-ocrv6-small
```

本机原始对照保存在忽略目录 `tmp/ocr-analysis-20261106/`；本轮原始日志、分配与启动结果、汇总 JSON、CLI 测试和检查日志在 `tmp/ocr-optimization-20261106/`。测试图与识别内容仅保存在本地。

Debian 13/amd64 无 GPU 时使用 CPU；Go SIMD 在支持的 amd64 CPU 上检查 AVX，未启用实验或不支持则标量回退。上述性能数字来自 M2 Pro，尚无 Debian 真机性能结论。切换模型/provider/批次后需用业务标注集及目标并发重新确认吞吐、质量与内存。

## 验证

- `go test ./...` 通过。
- `GOEXPERIMENT=simd CGO_ENABLED=1 go test -tags vips ./... -count=1` 通过，启用真实 medium OCR、分类模型及 CoreML 测试。
- small 的完整 OCR 原生套件通过，涵盖四格式、批量/串行、混合 provider、取消、关闭、输出重试和池清零。
- OCR、ONNX、计时、API handler 的原生 `-race` 检查通过。
- 默认构建和 vips/SIMD 构建的全量 `golangci-lint run ./...` 均为 0 issues；单独 `--enable-only dupl`、配置格式化检查及 `git diff --check` 通过。
- 生产 CLI 构建通过，medium 连续 JPEG 两次、small 四种格式均成功。重复 JPEG 第二次记录 `buffer_reused=8`、`buffer_allocated=0`，每次恰好一条 `OCR timing`。
- medium 混合 provider 的登机牌 CLI 测试返回 33 行，记录 `recognition_batches=16`、`recognition_batch_max=3`、`tensor_backend=go_simd`，确认实际批量执行生效。

性能接受条件是保持图像预处理及输出语义，记录完整请求和资源成本，不把工作移到不可见的后台；批量、预热、缓存可分别关闭，两个 provider 可各自回退 CPU。若目标负载延迟、吞吐或内存变差，应按阶段日志及同负载对照选择这些配置，避免只以 CPU 使用率判断收益。
