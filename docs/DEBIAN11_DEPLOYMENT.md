# Debian 11 x86_64 原生 CPU 部署

新模型所需完整 libvips 流水线、固定依赖及数值验收见 [当前模型说明](LIBVIPS_MODEL_PIPELINE.md)。


目标为 Debian 11（bullseye）x86_64，直接运行 Go 二进制。推理使用 CPU，不需要 Python、PyTorch、CUDA、libavif 或 libwebp。图片解码沿用项目中的 Go 实现。

## 兼容结论

当前 `onnxruntime_go v1.38.0` 的头文件声明 `ORT_API_VERSION=31`，部署固定使用官方 **ONNX Runtime CPU 1.31.1 linux-x64**。不能仅因模型使用 opset 17，就为当前 Go 绑定换用 1.17 / 1.20 等旧动态库；模型格式兼容与 C API 兼容是两层要求。

已下载官方 x64 发布包、校验 SHA-256，并读取其 ELF `.gnu.version_r` 中的真实依赖要求：

| 依赖 | 官方 1.31.1 x64 CPU 库最高要求 | Debian 11 基线 |
| --- | --- | --- |
| glibc | `GLIBC_2.28` | glibc 2.31 |
| libstdc++ | `GLIBCXX_3.4.22` | GCC 10 的 `GLIBCXX_3.4.28` |
| C++ ABI | `CXXABI_1.3.11` | `CXXABI_1.3.12` |

这些符号要求在 Debian 11 标准系统库的支持范围内，**不需要升级系统 glibc 或重编译 ONNX Runtime**。仍须在目标服务器检查动态依赖，并运行模型 fixtures 验收；不能将 macOS 上的验收视为 Linux 实机验收。

另一个兼容点是 Go 程序本身：本项目通过 CGO 调用 ONNX Runtime。若在较新的 Linux 上编译，二进制可能依赖 `GLIBC_2.34` 等新符号，在 Debian 11 上甚至无法进入 `main`。因此在 Debian 11 x86_64 构建机上构建，并将 `CGO_ENABLED=1`、`GOAMD64=v1` 固定下来。`GOAMD64=v1` 控制 Go 代码的指令基线，不能代替在目标 CPU 上对原生运行库的实际验收。

依据：[Go 绑定固定版本头文件](https://github.com/yalue/onnxruntime_go/blob/v1.38.0/onnxruntime_c_api.h)、[官方 ONNX Runtime 1.31.1 发布包](https://github.com/microsoft/onnxruntime/releases/tag/v1.31.1)、[Debian 11 libc6](https://packages.debian.org/bullseye/libc6)、[Debian 11 libstdc++6](https://packages.debian.org/bullseye/libstdc%2B%2B6)、[GCC 符号版本表](https://gcc.gnu.org/onlinedocs/libstdc++/manual/abi.html)。符号要求来自本次发布包的实际检查，不是对所有 Linux 发行包或未来版本的通用承诺。

## 在 Debian 11 构建

构建机可以与运行服务器分开，但同样使用 Debian 11 x86_64。先准备编译及下载工具：

```bash
sudo apt-get update
sudo apt-get install -y --no-install-recommends \
  build-essential ca-certificates curl git binutils libstdc++6 libgcc-s1 \
  cmake meson ninja-build nasm pkg-config libglib2.0-dev libexpat1-dev \
  libjpeg62-turbo-dev libpng-dev libwebp-dev libexif-dev liblcms2-dev zlib1g-dev
```

按 [Go 官方安装说明](https://go.dev/doc/install) 安装满足仓库 `go.mod` 的 Go 1.27 或更新版本。不要直接使用 Debian 11 仓库中的旧 Go。构建前端还需安装符合 Vite 要求的 Node.js：推荐 22.12+ 的 22.x 或更新 LTS；当前依赖也接受 20.19+ 的 20.x。运行服务器在拿到构建产物后不需要 Go、Node.js、npm 或 C 编译器。

在仓库根目录执行：

```bash
bash scripts/build-debian11.sh
# 或输出到一个尚不存在的目录：
bash scripts/build-debian11.sh /srv/builds/inferkit-debian11-amd64
```

脚本会验证构建机的系统和架构、Go / Node 版本，下载并校验固定的 CPU 库、核对 Go 绑定与原生头文件的 C API 版本，执行 `npm ci` / `npm run build`，启用 CGO 编译，再对程序和运行库执行 ELF 符号基线检查和 `ldd` 检查。使用系统默认 Go / npm 缓存，不创建仓库级依赖缓存。拒绝覆盖已有输出目录。

默认产物如下，**不包含本机配置、密钥、数据库内容或模型文件**：

```text
dist/debian11-amd64/
├── inferkit
├── image/              # 私有 libvips/libheif/dav1d/Highway和许可
├── runtime-packages.txt # 目标机需要安装的系统运行库
├── onnxruntime/        # 完整官方 CPU 包，保留 lib/ 中的版本文件和符号链接
├── check-abi.sh
├── inferkit.service
└── build-info.txt
```

`dist` 为构建产物目录，不提交 Git。前端资源已嵌入 `inferkit`，部署时无需额外运行 Vite。

网络受限时可提前下载官方包，并通过以下方式离线提供 **ONNX Runtime 包**；首次 Go / npm 依赖下载仍需可用网络或已填充的系统默认缓存：

```bash
ONNXRUNTIME_ARCHIVE=/path/to/onnxruntime-linux-x64-1.31.1.tgz \
  bash scripts/build-debian11.sh
```

下载地址：`https://github.com/microsoft/onnxruntime/releases/download/v1.31.1/onnxruntime-linux-x64-1.31.1.tgz`

固定 SHA-256：`c849e36a1ef3e22d4ff217b9d1ce4a3cf5b9467b975610913ae959ad5b16bdc9`。脚本不接受未通过该摘要校验的替代包，也不会因下载失败自动降级版本。

## 部署目录和配置

目标机安装运行依赖；`binutils` 供部署检查使用，`locales` 用于运行库要求的 UTF-8 locale：

```bash
sudo apt-get update
sudo apt-get install -y --no-install-recommends \
  ca-certificates libstdc++6 libgcc-s1 binutils locales \
  libglib2.0-0 libexpat1 libjpeg62-turbo libpng16-16 libwebp6 \
  libwebpmux3 libwebpdemux2 libexif12 liblcms2-2 zlib1g
sudo localedef -i en_US -f UTF-8 en_US.UTF-8
```

默认构建包含原生图片库，并通过 `GOEXPERIMENT=simd` 启用 Go 张量内核；libvips 8.18.7 与模型清单必须一致。

将构建产物整体复制到 `/opt/inferkit`，同时将模型包复制到 `/opt/inferkit/models/artwork_fp32`。复制目录时保留符号链接，如使用 `cp -a` 或 `rsync -a`。不要只复制 `libonnxruntime.so` 这个链接而漏掉它指向的版本文件。

创建服务账户及工作目录（已存在的账户不必重复创建）：

```bash
sudo useradd --system --user-group --home-dir /var/lib/inferkit \
  --shell /usr/sbin/nologin inferkit
sudo install -d -o root -g inferkit -m 0750 /etc/inferkit
sudo install -d -o inferkit -g inferkit -m 0750 /var/lib/inferkit
```

将已经配置好的应用配置安装为 `/etc/inferkit/app.toml`，归属 `root:inferkit`、权限 `0640`。保留当前鉴权、数据库、审核账号等实际设置，调整以下平台相关项：

```bash
sudo install -o root -g inferkit -m 0640 /path/to/app.toml /etc/inferkit/app.toml
sudo chown -R root:inferkit /opt/inferkit
sudo chmod -R u=rwX,g=rX,o= /opt/inferkit
```

```toml
port="8081"
mode="release"

[logger]
level="info"

[server]
shutdownTimeout="30s"

[storage]
path="/var/lib/inferkit"

[image.filter]
model="/opt/inferkit/models/artwork_fp32"
library="/opt/inferkit/onnxruntime/lib/libonnxruntime.so.1.31.1"
# CPU 线程、图片并发和请求容量根据机器资源自动分配。
timeout="30s"
maxBytes=10485760
maxPixels=20000000
cacheEntries=1024
cacheTTL="5m"
```

这里是合并到现有配置的片段，不是完整鉴权配置。必须替换原来的 macOS 模型 / 动态库路径；使用绝对 `library` 路径后无需设置 `LD_LIBRARY_PATH` 或运行 `ldconfig`。完整部署保留 `image.filter.recordResults` 和 `[image.review]` 设置，MySQL 迁移按 [IMAGE_REVIEW.md](IMAGE_REVIEW.md) 执行；纯推理部署应关闭结果采集并去掉数据库配置。

systemd 模板启用了 `ProtectHome=true`，模型和密钥不能继续存放在 `/home`、`/root` 或 macOS 路径。服务账户需要能读取模型、原生库、配置以及启用配置加密时的外部 keyring。应用目录由 root 管理；需要的持久写入统一放在 `/var/lib/inferkit`，上传临时文件由 `PrivateTmp` 提供独立的临时目录。

## 上线前验收和启动

先在目标服务器检查程序和动态库：

```bash
bash /opt/inferkit/check-abi.sh \
  /opt/inferkit/inferkit \
  /opt/inferkit/onnxruntime/lib/libonnxruntime.so.1.31.1 \
  /opt/inferkit/onnxruntime/lib/libonnxruntime_providers_shared.so
```

ABI 检查应全部通过，`ldd` 输出不能含 `not found`。脚本可识别常见的“在较新系统编译”“错拿 ARM64 动态库”“缺少 C++ 运行库”等问题，但不能代替实际加载模型。

模型部署包同时复制 `validation.json` 和 `fixtures/` 后，用实际服务身份执行：

```bash
sudo -u inferkit env LANG=en_US.UTF-8 \
  /opt/inferkit/inferkit --config /etc/inferkit/app.toml image-filter validate
```

它验证模型加载、原始输入张量、解码 / 预处理和概率对照，任一失败返回非零退出码。再使用 JPEG、PNG、WebP、AVIF 真实业务样本运行 CLI 或接口验收。现有模型包的 opset=17、IR=8 可以继续使用，不需为此重新导出。CPU 差异导致的性能与极小浮点误差以目标机验收为准。

安装并启动 systemd 服务：

```bash
sudo install -m 0644 /opt/inferkit/inferkit.service /etc/systemd/system/inferkit.service
sudo systemctl daemon-reload
sudo systemctl enable --now inferkit
sudo systemctl status inferkit
sudo journalctl -u inferkit -n 100 --no-pager
```

模板使用 `image-filter serve`，不会初始化 Redis 或 Queue；已配置的 MySQL 结果采集与审核页面仍正常启用。若需要完整服务的其他能力，将 `ExecStart` 最后的 `image-filter serve` 改成 `serve`，并准备相应依赖。CPU 推理模型仍只初始化一次，不把独立的模型验收命令放进 `ExecStartPre`。

Nginx 沿用当前反向代理配置与 `X-Forwarded-Proto`。systemd 的 `TimeoutStopSec=45s` 为示例中的 30 秒应用优雅停机留出余量；保留自定义停机时限时应同步调整。

## 自动检查与本次验证范围

`.github/workflows/debian11.yml` 在 Debian 11 容器内编译原生 x86_64 二进制，检查实际 ELF / 动态依赖，执行 Go 绑定的原生运行库初始化、版本和张量生命周期测试，以及项目图片解码和 HTTP 测试。容器仅用于 CI 的构建环境，不是生产部署方式。私有模型未提交 Git，因此完整模型 fixtures 仍需在目标机运行。

部署脚本的故障路径测试：

```bash
python3 -B -m unittest discover -s scripts/tests -v
# 额外验证官方离线包解压与符号链接保留：
ONNXRUNTIME_TEST_ARCHIVE=/path/to/onnxruntime-linux-x64-1.31.1.tgz \
  python3 -B -m unittest discover -s scripts/tests -v
```

当前开发环境为 macOS ARM64，未在此环境执行 Linux 二进制或实际启动 Debian systemd 服务；已检查官方库的 ELF 依赖，并验证校验失败拒绝发布、既有目录保护、错误架构和过新 ABI 拒绝等脚本行为。新 CI 需要推送后由 GitHub Actions 实际执行，目标服务器也应完成上述验收。


## 图片检测与 OCR 自动资源分配

启用 OCR 后，不再需要配置 `image.ocr.concurrency`、`image.ocr.threads` 或 `image.ocr.maxRequests`。程序在启动时根据可用 CPU、cgroup v1/v2 层级限额及内存预算自动分配，并行识别文字行，同时保留有限队列及超时背压。配置、日志字段、复测方法和资源变化后的重启要求见 [OCR 自动资源说明](OCR.md#cpu-与内存自动分配)。图片分类同样已改为自动分配，删除 `image.filter` 下的这三个旧执行参数，说明及复测方法见 [图片检测自动资源分配](GO_ONNX_SERVER.md#自动资源分配)。
