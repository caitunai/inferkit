# JPEG 原生处理 A～F 对照

本轮使用日志对应的原文件 `tmp/images/test1.jpeg`：199631 字节、1080×1864、多扫描 JPEG。另测 `test2.jpeg`（896×1152，多扫描）和 `test.jpeg`（1070×680，单扫描），并对 PNG/WebP/AVIF 及 11 组模型样本检查像素与概率。原图、模型和生产处理参数均未修改；候选只存在于独立实验可执行文件中。

环境为 macOS M2 Pro、10 核、libvips 8.18.7、mozjpeg 4.1.5。分类模型摘要为 `9d28cca25c765439e35cabc2f435a678e2ae996d45bacd79e434ca7afc3abe50`。

## 方法和候选

独立 C 程序直接复用生产 `native_vips.c` 的预检、取消控制、物化、计时和清理；只在编译实验程序时包装三个公开图像操作，不给应用增加实验配置。结果中的 A 逐字节对照实际 Go/libvips 生产入口，17 个样本均完全一致。

```text
只读加载同一份压缩字节
  → 每进程固定 libvips 线程数，预热 5 次
  → 原有格式/动画/页数/尺寸检查
  → 选择 A～F 的原生处理操作
  → 同样的 320×320 白底 RGB 输出
  → 原生对象、像素及取消控制释放
  → 记录每请求延迟、吞吐、CPU、进程峰值 RSS
```

| 候选 | 本次实际实现 |
| --- | --- |
| A | 当前 thumbnail_buffer + 颜色/alpha/白底处理 + 最终单次 write_to_memory；1 线程 |
| B | JPEG 强制 shrink=4，然后 thumbnail_image 和原有后处理；其他格式保持 A |
| C | 与 A 相同；当前生产已经使用 thumbnail_buffer，作为重复对照 |
| D | 基于 A，仅在已是 3 通道 sRGB uchar 时绕过色彩操作；相同像素格式的 cast 直接引用原图，其余仍调用原操作 |
| E | 与 A 相同；生产原本就在末端单次物化，作为重复对照 |
| F2/F4/F8 | A 分别设置 2/4/8 个 libvips 线程 |

三轮交替候选顺序；每轮单调用者 100 次，8 调用者各 100 次（合计 800）。共测 64800 次原生处理，不含预热。表中 P50/P95/P99 是各轮相应分位数的中位值；阶段时间是每轮均值的中位值。CPU 的 100% 表示一个核，RSS 包含整个实验进程，不能当成单请求内存。

本轮只测**原生图片处理生命周期**，不含 Go 读取、摘要、张量、HTTP、缓存或 ONNX 推理。日志中的 30.72/31.61ms 是不同运行条件下的单次观察；不能用它减去本轮预热结果来宣称优化收益。CPU/温度/调度和请求间隔等影响未逐项隔离，原生结果也不等于 API 端到端结果。

## 日志对应图片的结果

| 候选 | 单请求 P50 | P95 | P99 | 8 调用者吞吐（张/秒） | 8 调用者 P99 |
| --- | ---: | ---: | ---: | ---: | ---: |
| A | 11.815ms | 12.316ms | 12.561ms | 307.5 | 34.196ms |
| B | 10.680ms | 11.130ms | 11.250ms | 343.8 | 29.599ms |
| C | 11.836ms | 12.248ms | 12.389ms | 308.4 | 32.685ms |
| D | 11.714ms | 12.302ms | 12.531ms | 327.8 | 30.616ms |
| E | 11.795ms | 12.340ms | 12.693ms | 309.1 | 32.369ms |
| F2 | 11.698ms | 12.012ms | 12.206ms | 290.1 | 34.930ms |
| F4 | 11.745ms | 12.378ms | 12.414ms | 278.3 | 36.571ms |
| F8 | 12.007ms | 12.602ms | 12.768ms | 237.0 | 41.465ms |

- B 的单请求 P50 降低约 9.6%，8 并发吞吐提高约 11.8%；并没有消除大部分处理时间。
- D 单请求 P50 仅改善约 0.9%，不足以认定单图有明显提速；8 并发吞吐提高约 6.6%、P99 降低约 10.5%。
- F2 单图约快 1%，但 8 并发吞吐下降约 5.7%。F8 单图更慢，并发吞吐下降约 22.9%；CPU 从 A 的约 656% 增至约 687%，性能反而下降。
- C/E 与 A 本来就是相同算法，差异应按测量波动理解，不能当成新优化。

A 的原生阶段均值约为：header 0.067ms、prepare 0.585ms、eval 11.058ms、cleanup 0.156ms。B 改为 270×466 解码（A 为 540×932），eval 仍约 10.073ms；D 的 eval 约 11.035ms。多数时间仍留在实际像素求值中。当前数据不足以把剩余时间全部归给 JPEG 熵解码或缩放某一个函数。

另两张图的单请求 P50：

| 图片 | A | B | D | F2 |
| --- | ---: | ---: | ---: | ---: |
| test2.jpeg | 10.260ms | 9.041ms | 10.094ms | 9.873ms |
| test.jpeg | 3.837ms | 3.172ms | 3.712ms | 3.582ms |

它们的 8 并发下，D 吞吐分别提高约 1.7% / 9.6%，F2/F4/F8 均低于 A。没有发现适合统一改成多线程的结果。

## 像素与实际模型验收

17 个输入 × 8 个候选，共 136 组输出；分别用 Go SIMD 打包张量，在 CPU 和 CoreML 上各运行一次实际模型对照，两个后端各 136 组。先验证 A 与生产字节完全一致，再比较候选与 A。

| 候选 | 像素对照 | CPU 模型概率最大绝对差 | CoreML 模型概率最大绝对差 | 类别 |
| --- | --- | ---: | ---: | --- |
| B | 3 张真实 JPEG 改变；其余 14 个输入一致 | 0.03091921 | 0.03091867 | 本组样本均未改变 |
| C/D/E/F2/F4/F8 | 17 个输入全部逐字节一致 | 0 | 0 | 全部一致 |

B 对 test1.jpeg 的最大差是 **18 个 RGB 灰度级**，约 90062 个分量改变，RGB RMS 差约 1.136；其最大概率绝对差约 **0.030919（3.09 个百分点）**。对 test2.jpeg / test.jpeg 的最大像素差分别为 130 / 231。类别暂未改变不代表可以继承现有精度验收。

特别注意：本组 11 个模型 fixtures 的 B 输出也全部一致，因为其中唯一的 JPEG 是 4000×3000，A 原本就选择 1/4 解码。仅验收这 11 组无法发现强制 1/4 对中等尺寸 JPEG 的影响，所以加入原始真实图片对照是必要的。

## 本轮结论

1. **不将 B 合入现有模型默认路径**：超过已确认的最多 1 个灰度级容差，也不符合 auto_by_loader 的训练约定。若以后训练/部署统一采用另一预处理规则，需要新 profile 和业务精度评估。
2. **C/E 已经实现**，没有待消除的重复完整物化。
3. **D 是保持当前输入的候选**，本组样本输出完全一致，主要收益出现在并发吞吐；单图仍受 eval 阶段限制。本轮保留为实验，尚未修改生产色彩处理。
4. **F 保留生产的单图 1 线程和外层自动并发**。本次 macOS 结果不支持统一提高线程数，未来 Debian/libjpeg-turbo 仍需单独实测。

没有降低图片限制、改动模型、放宽验收标准，也没有把实验参数写入 `.app.toml`。本轮未测 Linux，也未做混合格式的 HTTP 全请求压测；不能宣称服务器或完整接口获得同样幅度的提速。

## 复跑

需要与模型匹配的原生依赖、C 编译器、pkg-config 和 Python 3 标准库；不需要 pyvips 或 Python onnxruntime。输出必须使用新目录，RGB 结果包含原图内容，应保存在忽略提交的本地目录。

```bash
python3 -B scripts/benchmark-image-native.py \
  --images tmp/images/test1.jpeg tmp/images/test2.jpeg tmp/images/test.jpeg \
  --quality-sources tmp/images/test.png tmp/images/test.webp tmp/images/test.avif \
  --fixtures tmp/artwork_fp32 \
  --output tmp/native-ab-new --samples 100 --rounds 3

GOEXPERIMENT=simd \
IMAGE_FILTER_TEST_MODEL="$PWD/tmp/artwork_fp32" \
IMAGE_FILTER_EXPERIMENT_DIR="$PWD/tmp/native-ab-new" \
IMAGE_FILTER_EXPERIMENT_PROVIDER=cpu \
go test -tags vips ./services/imagefilter -run '^TestNativeExperimentPixels$' -v
# macOS ARM 上将 provider 改成 coreml 可复跑第二个后端。
```

本次原始记录位于 `tmp/native-ab-round2/`：`raw.jsonl`、`summary.csv`、`report.json`、`model-quality-cpu.json`、`model-quality-coreml.json` 和 `pixels/`。这些模型和真实图片派生数据不会提交到 Git。
