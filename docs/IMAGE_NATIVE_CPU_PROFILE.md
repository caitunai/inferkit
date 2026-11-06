# native_eval 原生 CPU 采样

对日志对应的 `tmp/images/test1.jpeg` 完成采样：199631 字节、1080×1864、多扫描 JPEG，文件 SHA-256 为 `7079471a8070c8e5ca7deda74985409567b6a222fafc1370dcef5972f2583950`。当前自动解码缩小为 2，解码器输出 540×932。

**主要热点已确认为多扫描 JPEG 的熵解码和系数细化。** 当前图片约 84.7% 的 eval CPU 样本归属于 JPEG 解码器，其中最大单函数 `decode_mcu_AC_refine` 约 59.4%。libvips 重采样约 12.6%；颜色转换、最终补白和可确认的输出复制占比较低。

## 采样边界与计数方法

- 使用 macOS Xcode Time Profiler，约 1ms CPU 采样间隔，配置 `recordWaitingThreads=false`。
- 独立 C 程序复用当前生产处理链，单调用者、libvips 1 线程。先预热 5 次，再 attach 其 PID，避免应用启动、动态库加载和初始化干扰。
- 只在独立采样程序中包装最终 `vips_image_write_to_memory`，用 Points of Interest signpost 标记 `native_eval` 区间；不修改生产 CGO、图片参数或模型。
- 在同一 trace 时间轴上对齐 CPU 样本与 signpost，覆盖区间内的所有目标进程线程，包括实际像素工作线程。仅保留 `Running` 样本，并裁掉采样数据首尾各 0.5 秒。
- 三轮各记录 15 秒，合计观察到 3830 个 eval 区间，保留 **40144 个 CPU 样本**。预热输出与未采样程序逐字节一致。
- 按 PC/叶函数向调用者查找，分类互斥。解码器在重采样调用链下执行时归入解码，不能再同时计入重采样。重采样内部的边界 embed 也不当作最终补白。
- 泛用 region、tile cache、对象、分配和同步开销无法精确归给纯像素写入时，单独列为框架开销；不会把所有 memcpy/memset 都称为最终输出复制。

表中是 **CPU 样本权重占比**，不是各函数精确运行时长，也不是请求墙钟占比。不能直接乘日志中的 27.233ms 来拆出绝对毫秒数。采样带有自身开销，因此不把采样运行当作无采样性能基准。

## 当前多扫描 JPEG 的结果

| 分类 | eval CPU 样本占比 | 主要函数/含义 |
| --- | ---: | --- |
| JPEG 熵解码、扫描输入和系数细化 | **76.99%** | `decode_mcu_AC_refine`、`decode_mcu_AC_first`、`decode_mcu_DC_first`、`jpeg_fill_bit_buffer`、`consume_data` |
| JPEG IDCT | 5.39% | `jsimd_idct_4x4_neon`、`jsimd_idct_islow_neon` 等 |
| JPEG 其他解码器工作 | 1.34% | 其余解码、缓冲和控制函数及其通用辅助工作 |
| JPEG 色彩转换/上采样 | 0.98% | 主要为 `jsimd_ycc_extrgb_convert_neon`；这是解码器内部 YCbCr→RGB 等工作 |
| libvips 重采样及其 region/边界处理 | **12.63%** | 横向/纵向 `vips_reduceh` / `vips_reducev`，包括 Highway NEON 内核及相应辅助工作 |
| libvips 额外色彩变换 | 未命中样本 | 当前 JPEG 的输入已是相应 sRGB 路径；不能由“未命中”推导任意输入的色彩处理都为零 |
| 可明确归属的最终补白/输出像素复制 | **0.25%** | 直接绘制/复制相关叶函数，已排除重采样内部边界处理 |
| 框架、缓冲、区域调度和同步 | 2.41% | 不强行算成某个像素算子的工作 |
| 其他 | 0.005% | 剩余无法明确归类的样本 |

JPEG 四类合计约 **84.70%**。三轮熵解码/系数处理占比为 76.89%、77.01%、77.05%，主要热点稳定。重采样占比为 13.00%、12.75%、12.15%。

主要叶函数：

| 函数 | 占 eval CPU 样本 |
| --- | ---: |
| `decode_mcu_AC_refine` | **59.45%** |
| `decode_mcu_AC_first` | 6.58% |
| `decode_mcu_DC_first` | 3.52% |
| `jsimd_idct_4x4_neon` | 3.13% |
| `jsimd_ycc_extrgb_convert_neon` | 0.95% |

`vips_image_write_to_memory` 的包含子调用耗时很高，是因为它触发整个延迟执行图，不能据此认定内存复制很慢。本次保留框架开销单列后，明确的最终补白/输出复制只占约 0.25%，没有成为主要热点。

JPEG IDCT 和 libvips 重采样已有 NEON 路径；主要的 `decode_mcu_AC_refine` 位于原生 JPEG 解码库。增加 Go 张量 SIMD 或继续简化最后的 pad 无法直接覆盖这组主要热点。这个结论只针对当前输入和当前原生库，不等于所有 JPEG 都如此。

## 对照结果

每个对照另采样一轮 15 秒：

| 对照 | 样本数 | JPEG 熵/系数 | JPEG IDCT | libvips 重采样 | 说明 |
| --- | ---: | ---: | ---: | ---: | --- |
| 当前图 A，自动 1/2 | 40144，三轮 | 76.99% | 5.39% | 12.63% | 生产参数 |
| 同图 B，强制 1/4 | 13429 | **84.67%** | 2.70% | 7.88% | 仅隔离实验；此前已确认像素/概率超出验收约定 |
| 单扫描 `test.jpeg`，1070×680 | 11527 | 22.34% | 8.87% | **51.07%** | 不同尺寸和内容，不能据此量化多扫描本身的额外绝对耗时 |

同图强制 1/4 后，IDCT 和后续重采样占比下降，但熵/系数处理仍主导，说明缩小解码不会绕过这部分工作。这也与此前 [A～F 测量](IMAGE_NATIVE_AB.md) 中仅约 9.6% 的单图提速相符；这里没有把 CPU 样本占比变化当作新的延迟测量。

单扫描对照图的主要热点转为重采样，表明应按图片类型和实际内容定位。对当前多扫描图，应优先研究 JPEG 多扫描解码实现或数据源编码方式；重采样属于第二优先级。当前结果不支持继续把主要精力放在色彩 no-op、补白和最终复制上。

## test.jpeg 单独复测

再次确认文件未变化：85852 字节、1070×680，SHA-256 仍为 `6ad384ada1a8ce65a94d6de3bec3824687e7b5554e3fe53dcdbbfcdecc676aa6`，`jpeg_multiscan=false`、实际 `jpeg_shrink_on_load=1`。既有 15 秒、11527 样本的 CPU 采样仍对应此文件；本次另外预热后做三轮、每轮 100 次原生计时。

完整原生流程的三轮中位 P50/P95/P99 为 **3.666 / 3.753 / 3.967ms**。各阶段的每轮均值再取中位值：

| 阶段 | 耗时 |
| --- | ---: |
| header | 0.061ms |
| prepare | 0.551ms |
| eval | **2.927ms** |
| cleanup | 0.142ms |

均值和 P50 是不同统计量，不应要求它们严格加总相等。这是原生流程计时，仍不含 HTTP 和推理。

eval 内重采样占 **51.07%** 的 CPU 样本；JPEG 熵解码/系数约 22.34%、IDCT 8.87%、其他解码 2.97%、JPEG 色彩/上采样 7.06%，解码器合计约 **41.23%**。框架/缓冲约 7.11%，明确补白/输出复制约 0.57%。因此当前文件首先应看重采样，其次才是 JPEG 解码，不能套用 test1.jpeg 的多扫描热点结论。

这里未做解码缩小符合当前 libvips thumbnail 的默认选择：1070/320≈3.34，尚未达到其选择 1/2 解码缩小的阈值。不能将 shrink=1 视为配置失效。独立计时记录在 `tmp/native-test-jpeg-confirm/native-timings.json`。

本次未改变生产默认参数，也未切换 JPEG 库或模型。没有 Linux/debian11 实测数据；未来 libjpeg-turbo 的函数占比应在服务器独立采样。

## 复跑和检查

macOS 需要完整 Xcode、Time Profiler、C 编译器、pkg-config 及当前原生依赖。Python 工具使用标准库。Instruments 使用系统默认缓存；若命令受沙箱限制，需要允许访问该缓存和自己创建的采样子进程。工具只 attach 并结束其自行创建的目标进程。

```bash
python3 -B scripts/profile-image-native.py \
  --image tmp/images/test1.jpeg \
  --output tmp/native-cpu-new --seconds 15 --rounds 3

python3 -B -m unittest discover -s scripts/tests -p test_native_profile.py -v
```

工具会读取 typed XML 的 id/ref 引用，保留 raw `.trace`、`samples.xml`、`intervals.xml`、每轮 `report.json` 和合并 `summary.json`；记录输入/原生源码摘要及基准解码参数。解析测试覆盖重复计数、解码器内颜色转换、重采样内部 embed、通用复制归属及时间/运行态过滤。

本次数据位于忽略提交的 `tmp/native-cpu-test1/`，另有 `tmp/native-cpu-single-scan/` 和 `tmp/native-cpu-test1-quarter/` 对照。trace 可能包含设备标识及本地路径，RGB 文件包含原图派生内容，仅保存在本地，不提交到 Git。
