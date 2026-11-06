# ONNX 启动与 CPU 预算验证

2026-11-06，Apple M2 Pro / 10 CPU / 32 GiB，macOS 27.2，Go 1.27.3，`GOEXPERIMENT=simd`，libvips 8.18.7，ONNX Runtime 1.31.1。模型 SHA256：`9d28cca25c765439e35cabc2f435a678e2ae996d45bacd79e434ca7afc3abe50`。

模型、FP32 精度和 libvips 预处理保持相同。计时基准按顺序运行，没有与构建、lint 或其他测试并行。表格每列为三轮对应指标的中位数，不是合并全部样本后的分位数。结果仅代表本机，不代表未来 Debian amd64 服务器。

## CoreML 启动

`BenchmarkCoreMLStartup`：每个场景每轮新建并关闭 10 次服务，共三轮；CoreML/ALL、自动 2 线程。分别记录 NewContext 完成时间和紧接其后的第一次原生 Run；输入为相同的归一化白色画布。初始化时间包含模型读取、校验、会话/张量创建、启动日志和可选预热；首次推理不包含图片解码、libvips、HTTP 或排队。

缓存场景使用此前已经生成的编译产物。它测量跨会话/服务重启的缓存复用，不是空缓存首次生成的成本；第一次生成仍须完整编译。新会话在相同进程内创建，不能等同于整台机器冷启动。

| 编译缓存 | 预热次数 | 初始化均值 ms | 首次 Run P50 ms | P95 ms | P99 ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| 关闭 | 0 | 241.20 | 29.440 | 31.610 | 31.610 |
| 关闭 | 3 | 285.00 | 3.333 | 5.995 | 5.995 |
| 复用 | 0 | 53.65 | 30.140 | 32.180 | 32.180 |
| 复用 | 3 | 94.02 | 3.853 | 5.950 | 5.950 |

这里的初始化均值先在各轮内求平均，再取三轮中位数。每轮只有 10 次，P95/P99 都落在最大样本处，不能用作生产尾延迟目标。

预热把首次推理成本移到就绪之前；编译缓存降低会话转换和加载成本。它们并不保证提高稳定状态吞吐，也不保证消除设备空闲恢复延迟。`TestNativeCoreMLCompilationCacheReuse` 在两个依次创建、关闭的缓存会话上分别通过模型包全部 11 组图像/张量验证，目录身份保持一致。

## CPU 完整图片处理

`BenchmarkFilterSizedLoad`：同一组 8 张不同内容的 1129×774 PNG，libvips 预处理、SIMD 张量写入、CPU 推理和结果构建均计入，不含 HTTP、文件网络读取和数据库持久化。结果缓存容量为 0；批次内不同图片没有同内容合并。每场景每轮 30 批，各三轮；1 调用者每轮 30 次，8 调用者每轮 240 次。每轮正式计时前使用相同图片预热。

两种方案共享一个会话，均使用生产的有界自旋 1000µs/backoff=8/force-stop=1；2 线程配置 9 个处理位，4 线程配置 7 个处理位。每批结束后再发下一批，是固定批次闭环负载，不是开放到达的生产 HTTP 压测。

| ORT 线程 | 调用者 | 图片/s | P50 ms | P95 ms | P99 ms |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | 1 | 33.48 | 29.81 | 30.53 | 30.93 |
| 4 | 1 | 46.04 | 21.42 | 23.00 | 24.84 |
| 2 | 8 | 105.60 | 70.49 | 76.07 | 77.74 |
| 4 | 8 | 101.30 | 64.50 | 79.26 | 82.59 |

4 线程的单请求 P50 下降约 28%，但 8 并发吞吐下降约 4%，P99 增加约 6%。因此自动起点以单图延迟为取舍，并不承诺所有指标同时改善。吞吐优先的部署可显式对照 `threads=2`、`threads=4` 与自动方案。Linux CPU 的最终选择仍应在实际服务器上比较包含 HTTP、真实图片和后台业务的完整负载。

## 重现

设置真实模型目录，并使用系统默认工具缓存：

```bash
export IMAGE_FILTER_TEST_MODEL=/absolute/path/to/artwork_fp32
export IMAGE_FILTER_TEST_COREML=1
GOEXPERIMENT=simd CGO_ENABLED=1 go test -tags vips ./services/imagefilter \
  -run '^$' -bench '^BenchmarkCoreMLStartup$' -benchtime=10x -count=3
GOEXPERIMENT=simd CGO_ENABLED=1 go test -tags vips ./services/imagefilter \
  -run '^$' -bench '^BenchmarkFilterSizedLoad/threads_(2|4)/callers_(1|8)$' \
  -benchtime=30x -count=3
```

CoreML 需要 macOS ARM 和原生设备/系统编译缓存权限。CPU 场景不需要 CoreML 开关。稳定部署验收应增加样本量，分别检查启动、间隔请求和连续负载，并验证业务图片概率、请求取消与资源归还。
