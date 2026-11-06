# 可移植 Go SIMD 与发布检查

Go 1.27 的 `simd` 包提供架构和向量宽度无关的操作，底层硬件选择由标准库负责。[官方文档](https://pkg.go.dev/simd)

原 OCR 代码使用 `simd/archsimd` 的 `ReduceMax/ReduceMin`，这些方法仅在 arm64 API 中存在，因此 macOS 本机通过而 Linux amd64 编译失败。本次将 OCR 和图片处理的 Go SIMD 运算统一到 `services/internal/vector`，只导入标准库 `simd`，移除原有 arm64/amd64 专用分支。

- OCR 按 `Float32s.Len()` 处理向量块，融合概率校验和 argmax。最后通过 `Store` 读取各 lane 的最大值与首索引，完成少量标量归约，不依赖架构专用归约指令。NaN、无穷值、范围检查、并列最大值的首索引及 CTC 规则保留。
- RGB 先打包字节，再做统一的掩码、移位和浮点归一化；RGBA 使用整数合成白底，保持原来的舍入。固定点重采样继续使用已证明不会溢出的系数与累加边界。
- 不假设每个向量固定包含四个元素。标准库报告 `simd.Emulated()` 时，业务层选用已有标量路径，日志记录 `go`；实际硬件路径记录 `go_simd`。`purego` 和未启用实验的构建也走标量。
- libvips、dav1d、Highway、ONNX Runtime 自身的原生优化及模型预处理规则没有改变。此前的 M2 性能报告不是本次实现的重新测量结果。

构建命令保持：

```bash
GOEXPERIMENT=simd CGO_ENABLED=1 go build -tags vips -o inferkit .
```

共享内核只依赖 Go 标准库，可独立验证，不需要 ONNX 或 libvips 的目标平台库：

```bash
GOEXPERIMENT=simd CGO_ENABLED=0 go test ./services/internal/vector -count=1
GODEBUG=simd=0 GOEXPERIMENT=simd CGO_ENABLED=0 go test ./services/internal/vector -count=1

GOEXPERIMENT=simd CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go test -c -o /tmp/vector-amd64.test ./services/internal/vector
GOEXPERIMENT=simd CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go test -c -o /tmp/vector-arm64.test ./services/internal/vector
```

交叉编译只证明目标架构编译成功，不等于目标 CPU 上执行通过。CI 在 Ubuntu amd64 上运行直接内核测试、标准库模拟执行测试，并交叉编译 arm64；本机 macOS arm64 运行原生和模拟测试。原服务测试继续比较标量与 SIMD 输出，包括所有透明度/通道值、短尾、非连续行、负系数、溢出回退和 CTC。

## 测试图片与 Git LFS

附件中的 `missing ispe` 和四格式 `invalid libvips image input` 是另一个独立问题：原图片按全局扩展名规则存在 LFS 中，默认 CI checkout 取到指针文本。已将 `services/imagefilter/testdata`、`services/imagepipeline/testdata` 和 `services/ocr/testdata` 下的小型 PNG/JPEG/WebP/AVIF 素材按图片后缀豁免 LFS，并重新存入普通 Git blob。业务 `.onnx`、其他图片和 `.f32` 的 LFS 规则保留。

部署脚本测试会检查这些图片的真实格式头，若再次收到 LFS 指针，会在昂贵的原生依赖构建之前明确失败。镜像仍通过 `.dockerignore` 排除业务模型和本机配置；发布前的 small 权重继续使用校验后的临时外部挂载，不打包进镜像。
