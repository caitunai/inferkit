# Debian 13 amd64 Docker 部署

当前镜像默认包含新模型所需的完整 libvips 流水线，详见 [当前模型说明](LIBVIPS_MODEL_PIPELINE.md)。


镜像基于官方 `debian:trixie-slim`（Debian 13），仅支持 `linux/amd64`。每次 Actions 构建会拉取最新基础镜像，跟进 Debian 13 的点版本；不使用可能在未来切换到 Debian 14 的 `debian:latest`。

Debian 13 是当前容器运行环境；原生库使用容器内的 glibc。宿主机需具备可用的 amd64 Docker Engine 和相应内核支持。Debian 11 专用构建、ABI 基线检查及 systemd 发布包已移除。

前端由 Node 24 构建，随后嵌入 Go 二进制；Go 使用 `golang:1.27-trixie`、CGO 和 `GOAMD64=v1` 编译。镜像包含固定的 ONNX Runtime CPU 1.31.1 及其系统依赖，通过共享脚本下载官方包、校验 SHA-256 并核对 C API 版本；另外打包 OpenVINO EP 1.8.1 的 CPU-only 运行库，支持显式选择 OpenVINO FP32。默认仍为 CPU EP。默认启用 `WITH_VIPS=true` 及 Go 标准 SIMD，并在私有前缀构建固定的 libvips 8.18.7、libheif 1.23.7、dav1d 1.5.4、Highway 1.4.0；JPEG 使用 Debian libjpeg62-turbo。

公开测试图片作为普通 Git 文件提交，避免 checkout 后留下 LFS 指针而使解码测试失败；模型权重继续使用 LFS，CI 不为测试拉取私有业务模型。[可移植 SIMD 与测试素材](PORTABLE_SIMD.md)

配置、训练模型、密钥和数据库内容通过外部配置或挂载提供。`.dockerignore` 使用构建输入白名单，排除 `.git`、本机 `.app.toml` / `.env`、模型权重、私钥、依赖目录和已有构建产物，避免它们进入镜像或 CI 构建缓存。

## GitHub Actions 构建和发布

工作流文件为 `.github/workflows/docker.yml`，名称是 **Debian 13 amd64 image**。构建和 lint 的 runner 均固定为 `ubuntu-24.04`，不跟随 `ubuntu-latest` 迁移；Docker 内仍使用 Debian 13。lint 使用 Go `1.27.x` 和 golangci-lint `v2.15.0`，与本机验证版本对齐。旧版 errcheck 会误判 `errors.AsType` 返回的匹配值，新版本已修正，不需要修改错误处理或放宽 lint 规则。[errcheck 的 AsType 支持](https://github.com/kisielk/errcheck/blob/v1.30.0/errcheck/errcheck.go#L399)

发布触发规则：

- 推送 `main` / `master`、推送 `v*` 标签时构建并发布；仓库默认分支更新 `latest`，版本标签生成同名镜像标签。
- 默认分支同时更新 `debian13` 标签，版本标签另外生成 `vX.Y.Z-debian13`；每次发布仍生成 `sha-<完整提交 SHA>`。
- PR 构建检查镜像，不登录任何镜像仓库、不推送。
- Actions 页面可手动执行；勾选 `no_cache` 可刷新全部构建层中的系统与依赖包。

GHCR 镜像名称自动取 `ghcr.io/<仓库所有者>/<仓库名>`，统一转为小写，不需要在 YAML 中修改用户名。GHCR 使用仓库的 `GITHUB_TOKEN` 和 `packages: write` 权限。首次启用时，确认组织 / 仓库允许该工作流发布 Packages。

构建发布成功后，可从该次 Actions 运行的 Summary 中直接复制镜像标签。

例如仓库为 `example/inferkit`，发布结果为：

```text
ghcr.io/example/inferkit:latest
ghcr.io/example/inferkit:sha-<完整提交SHA>
ghcr.io/example/inferkit:v1.0.0    # 推送对应 Git 标签时生成
```

工作流先构建并载入镜像，再检查 Debian 13、amd64、非 root 身份、程序动态依赖和 CLI 可执行性，最后推送**同一份已检查的镜像**，不会在测试后重新构建另一份。

构建过程在最终运行库环境中，以 UID 10001 检查 ONNX Runtime C API/张量生命周期、OCR CTC/SIMD 与关闭 AVX 后的回退，并验证 JPEG/PNG/WebP/AVIF 的 libvips 预处理、10 位 AVIF、缩小解码和透明补白。测试程序及其素材仅在中间构建层中，最终镜像只复制成功标记。

发布前运行 `scripts/test-inference-image.py`，在构建和缓存导出结束后下载并校验官方 PP-OCRv6 small 权重，挂载到已经构建好的镜像中。模型在宿主机临时目录，不进入镜像、构建上下文或 BuildKit 缓存。容器以非 root、只读根文件系统、禁用网络、2 CPU/4GiB 的预算执行，临时文件放在 `/tmp`；完成或超时都会清理本次容器和临时模型。

验收包含十二行合成文字的连续两次识别，以及四格式原有合成图片，必须满足：识别出文字、输入尺寸正确、两阶段实际使用 CPU、有限预热 3 轮、批量识别生效、重复输入复用缓冲区，以及 JPEG/WebP 使用 libvips 解码。缺少对应阶段日志或任何检查失败都会阻止发布。无 AVX 的机器允许 Go 标量回退；不以 SIMD 名称代替结果验收。

Intel AVX2 runner 还会执行三种含 OpenVINO 的阶段组合，对照 CPU 基线检查文字、行顺序、置信度（绝对差不超过 0.001）和坐标（各分量不超过 1 像素），要求 FP32 及实际 provider 日志。标准 GitHub x64 runner 也可能为 AMD，此时明确提示未执行 OpenVINO 推理验收，仅执行 CPU OCR 和 OpenVINO ELF 依赖检查；应在目标 Intel 服务器补跑下文的 `--with-openvino` 验收。

镜像以 amd64 原生 runner 构建，不需要 QEMU。中间层通过 GitHub Actions cache 复用。首次构建包含 Go / npm 依赖、前端、测试及原生库下载，后续构建会复用缓存。

首次发布的 GHCR Package 可能为私有。部署机需要先 `docker login ghcr.io`，使用具备该 Package 读取权限的凭据；也可由仓库管理员在 GHCR 中调整 Package 的可见性。工作流使用的 `GITHUB_TOKEN` 不应复制到部署机配置中。

### 同时发布到 Docker Hub

在 GitHub 仓库 **Settings → Secrets and variables → Actions** 中添加：

| 类型 | 名称 | 内容 |
| --- | --- | --- |
| Repository variable | `DOCKERHUB_USERNAME` | Docker Hub 登录用户名；设置后启用 Docker Hub 发布 |
| Repository secret | `DOCKERHUB_TOKEN` | 对目标仓库具有推送权限的 Docker Hub Access Token |
| Repository variable，可选 | `DOCKERHUB_IMAGE` | `namespace/repository`，如 `my-org/inferkit`；默认 `<DOCKERHUB_USERNAME>/<GitHub仓库名>` |

提前创建相应 Docker Hub 仓库，并保证登录账户和 Token 有推送权限。组织仓库通过 `DOCKERHUB_IMAGE` 指定；该变量也接受 `docker.io/namespace/repository`，不附带标签。镜像名称统一转为小写，Docker Hub 和 GHCR 使用同样的 `latest`、`debian13`、版本（含 `-debian13` 版本别名）和提交 SHA 标签。

例如 `DOCKERHUB_USERNAME=example`、GitHub 仓库名为 `inferkit` 时，还会发布：

```text
docker.io/example/inferkit:latest
docker.io/example/inferkit:sha-<完整提交SHA>
docker.io/example/inferkit:v1.0.0
```

未配置 `DOCKERHUB_USERNAME` 时，工作流显示提示并继续仅发布 GHCR。启用 Docker Hub 却缺少 Token 时，在构建前明确报错；PR 不读取或校验发布 Token，也不登录或推送。两端共用一次构建和检查结果，发布任一端失败都会使该次工作流失败，已推送到另一端的标签可能已经存在，修正问题后可重新运行。

部署时将 `INFERKIT_IMAGE` 改成 Docker Hub 镜像即可，Compose 其他配置保持一致：

```bash
export INFERKIT_IMAGE=docker.io/example/inferkit:latest
```

## 快速部署

部署服务器需要 amd64 Docker Engine 和 Docker Compose v2。将以下文件准备到一个部署目录，例如 `/srv/inferkit`：

```text
/srv/inferkit/
├── compose.yaml       # 来自 deploy/docker/compose.yaml
├── app.toml           # 从现有配置迁移，或复制 deploy/docker/app.toml.example 后填写
├── artwork_fp32/      # 完整分类模型包
│   ├── artwork.onnx
│   ├── deployment.json
│   ├── labels.json
│   ├── validation.json
│   └── fixtures/
└── pp-ocrv6-medium/   # 配套 OCR 模型与字典，也可切换为 small
    ├── det.onnx
    ├── rec.onnx
    └── dict.txt
```

现有配置应保留 API 鉴权的 `secret`、`audience`、`issuers` 和应用 `key`，以继续使用已有 Token。全新部署需要填写密钥；`key` 应为 32 个字符，`auth.api.secret` 至少 32 字节。示例文件故意不包含可直接使用的密钥。可分别使用 `openssl rand -hex 16`、`openssl rand -hex 32` 为新部署生成值。

修改模型和运行库为容器内路径：

```toml
[image.filter]
model="/models/artwork_fp32"
library="/opt/onnxruntime/lib/libonnxruntime.so.1"

[image.ocr]
enabled=true
modelSize="medium"
detectionModel="/models/ocr/det.onnx"
recognitionModel="/models/ocr/rec.onnx"
dictionary="/models/ocr/dict.txt"
library="/opt/onnxruntime/lib/libonnxruntime.so.1"
```

Compose 使用单个 `inferkit` service，执行一次 `serve`，在同一个进程中加载分类与 OCR 模型并共用 HTTP server。迁移现有 `app.toml` 时，需要显式启用 `image.ocr.enabled`，并保留现有 Redis/队列配置；从示例新建配置时，必须填写容器可达的 `redis.host` 及相应凭据。Redis 可继续使用现有服务器或其他容器。

运行账户 UID/GID 为 **10001:10001**。确保它能读取配置及模型。没有启用用户命名空间映射的 rootful Docker，可让配置归属 `root:10001`、权限 `0640`，模型目录允许该账户读取和进入；rootless Docker 请按下节设置映射后的权限，不能直接套用宿主机 GID 10001。命名数据卷自动使用镜像中 `/var/lib/inferkit` 的服务账户权限。启用配置加密时，外部 keyring 也需单独只读挂载，并将配置中的路径改为容器内路径。

在部署目录执行，替换为实际的小写仓库名称：

```bash
cd /srv/inferkit
export INFERKIT_IMAGE=ghcr.io/example/inferkit:latest
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100 inferkit
```

也可以在部署目录的 `.env` 中设置 `INFERKIT_IMAGE`。Compose 要求显式提供镜像名称，并拒绝将缺失的配置文件或模型目录自动创建成目录。可选变量：`INFERKIT_PORT`（宿主机端口，默认 8081）、`INFERKIT_CONFIG`（配置文件路径）、`INFERKIT_MODEL`（分类模型目录）、`INFERKIT_OCR_MODEL`（OCR 模型目录，默认 `./pp-ocrv6-medium`）。相对挂载路径以 Compose 文件所在目录为基准。

默认仅将 `127.0.0.1:8081` 映射到容器 `8080`，可以直接接入现有 Nginx 的 `proxy_pass http://127.0.0.1:8081`。两个页面为 `/image-filter`、`/ocr`，对应 API 为 `POST /api/image/filter`、`POST /api/image/ocr`，使用同一套鉴权配置。人工审核启用后的路径仍为 `/image-review`。继续传递 `Host` 和 `X-Forwarded-Proto`。

容器环境变量 `PORT` 默认是 `8080`，优先于 TOML 中的 `port`；Compose 固定保持该容器端口，`INFERKIT_PORT` 只改变宿主机映射。Compose 健康检查同时访问 `/image-filter` 和 `/ocr`，两者均成功才通过，不重复初始化模型或执行推理。启动宽限期为 90 秒，每轮检查超时为 8 秒。`docker compose ps` 可看到健康状态；Docker 的健康状态本身不会自动重启进程，当前重启策略在进程退出时生效。

容器根目录只读，配置和模型只读挂载，临时文件放在独立 `/tmp`，持久文件放在数据卷。Compose 已覆盖镜像命令为 `serve`；直接使用镜像、不使用 Compose 时，镜像的默认命令仍为独立分类服务 `image-filter serve`。

启用识别结果采集和人工审核时继续配置 MySQL，并在更新服务前按 [IMAGE_REVIEW.md](IMAGE_REVIEW.md) 执行 Atlas 迁移。容器内的 `localhost` 指向当前容器，数据库 / Redis 地址应改为容器可达的服务名称或地址。

## Rootless Docker 的模型读取权限

出现 `open /models/artwork_fp32/deployment.json: permission denied`，表示模型文件或路径中的目录权限阻止了读取，尚未进入模型校验和推理。镜像使用的容器 UID 10001 在 rootless Docker 下通常映射为宿主机 subordinate UID；宿主机当前用户拥有的文件，在容器内通常显示为 UID 0 所有。[Docker UID/GID 映射说明](https://docs.docker.com/engine/security/rootless/uid-gid-mapping/)

保留镜像用户，向其实际映射的宿主机 UID 授予模型只读权限即可。以下命令在部署目录、使用启动 rootless Docker 的同一个宿主机用户执行；需要宿主机安装 `acl`（Debian 可用 `sudo apt-get install acl`），且该用户拥有相应文件。配置路径及模型路径必须与 Compose 使用的环境变量一致；若写在 `.env` 中，请先将对应变量导入当前 shell。

先查看运行身份和内核映射。该命令覆盖入口为 shell，不启动应用，也不需要读取模型：

```bash
docker compose run --rm --no-deps --entrypoint sh inferkit \
  -c 'id; cat /proc/self/uid_map'
```

每一行分别为容器 UID 起点、外部 UID 起点和范围长度。标准 rootless Docker 的外部 UID 对应宿主机 UID；存在额外嵌套用户命名空间时，需由管理员继续转换到宿主机映射后再设置 ACL。

```bash
# 根据实际映射查出容器 UID 10001 对应的宿主机 UID，不硬编码 subuid 起点。
uid_map=$(docker compose run --rm --no-deps --entrypoint cat inferkit /proc/self/uid_map) || exit 1
mapped_uid=$(printf '%s\n' "$uid_map" | awk -v target=10001 \
  '$1 <= target && target - $1 < $3 { print $2 + target - $1; found=1; exit } END { if (!found) exit 1 }') || exit 1
test -n "$mapped_uid" || { echo "无法确定模型读取账户的宿主机 UID" >&2; exit 1; }

config_file="${INFERKIT_CONFIG:-./app.toml}"
model_dir="${INFERKIT_MODEL:-./artwork_fp32}"
ocr_dir="${INFERKIT_OCR_MODEL:-./pp-ocrv6-medium}"
setfacl -m "u:${mapped_uid}:r" "$config_file"
find "$model_dir" "$ocr_dir" -type d -exec setfacl -m "u:${mapped_uid}:rx" {} +
find "$model_dir" "$ocr_dir" -type f -exec setfacl -m "u:${mapped_uid}:r" {} +
```

ACL 为模型目录提供读取和进入权限，为文件提供读取权限，保留文件所有者及只读挂载。替换模型后需为新增文件重新设置 ACL；如果模型使用符号链接，应对其真实目标设置权限，并保证目标位于已挂载的模型目录中。配置加密 keyring 等额外挂载也需要相同的读取权限。

启动前从实际容器身份验证配置、两套模型及数据卷，再重建服务：

```bash
docker compose run --rm --no-deps --entrypoint sh inferkit -ec '
  for file in /etc/inferkit/app.toml /models/artwork_fp32/deployment.json \
    /models/ocr/det.onnx /models/ocr/rec.onnx /models/ocr/dict.txt; do
    test -r "$file" || { echo "cannot read: $file" >&2; exit 1; }
  done
  test -w /var/lib/inferkit
  echo "mount permissions OK"
'
docker compose up -d --force-recreate inferkit
docker compose logs --tail=100 inferkit
```

若文件权限测试仍失败，检查模型目录各级的 ACL、文件系统是否支持 ACL，以及服务器的 SELinux/AppArmor 拒绝记录。ACL 命令失败时应先处理失败原因，再启动服务。

## 验收和命令行

模型目录包含 `validation.json` 与 `fixtures/` 时，在启动前用同一镜像和挂载执行：

```bash
docker compose run --rm --no-deps inferkit \
  --config /etc/inferkit/app.toml image-filter validate
```

创建第三方 API Token（沿用现有配置的密钥和有效期规则）：

```bash
docker compose run --rm --no-deps inferkit \
  --config /etc/inferkit/app.toml api-token create
```

也可以将本地图片额外挂载后调用原来的 `image-filter` CLI，或使用浏览器 / API 上传图片和输入 URL。四种图片格式继续共用原有 CPU 推理服务。

发布后重新拉取并部署：

```bash
docker compose pull
docker compose up -d
```

回滚时将 `INFERKIT_IMAGE` 改为旧版本标签或镜像 digest，再次执行以上命令。`latest` 和同一提交的标签在重建基础镜像后可能改变；需要固定产物时使用对应镜像仓库返回的 `@sha256:...` digest。保留数据卷，避免使用 `docker compose down -v` 删除持久数据。

## 本地构建和验证范围

安装 Docker 后可从仓库根目录构建同一镜像：

```bash
docker buildx build --pull --platform linux/amd64 --load -t inferkit:debian13 .
```

在 ARM64 开发机上执行该命令需要 Docker 的 amd64 模拟支持；发布工作流只使用原生 amd64 runner。私有业务模型不会放进公开构建上下文，镜像中的原生库测试不代表已对真实模型完成 Linux 分类准确率或性能验收，部署时仍应执行上述 `validate`。

当前开发环境没有 Docker CLI / Engine，未在本机实际构建或推送镜像。完整 Linux 镜像构建、最终运行库检查及真实容器 OCR 验收会在工作流提交、推送到 GitHub 后执行。本机已验证 workflow 语法、部署检查脚本、Compose YAML，以及同一合成图片/四格式输入在 macOS CPU small 上的真实识别和验收逻辑；这不能替代 Linux 容器检查。

实现参考：[Debian 13 发布信息](https://www.debian.org/releases/trixie/)、[Docker 构建后测试再推送](https://docs.docker.com/build/ci/github-actions/test-before-push/)、[GitHub 发布镜像到 GHCR](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)、[Docker Hub 与 GHCR 同时发布](https://docs.docker.com/build/ci/github-actions/push-multi-registries/)。

## 配置 OCR 模型

`deploy/docker/compose.yaml` 已同时启用分类和 OCR 所需的命令及挂载，直接使用这一个 Compose 文件即可；原 OCR override 文件已移除。配置示例默认启用 OCR，使用 PP-OCRv6 medium。

如需使用 small，保留 `app.toml` 中现有鉴权密钥、Redis/队列设置，将 `[image.ocr]` 改为：

```toml
[image.ocr]
enabled=true
modelSize="small" # 默认仍是 medium；本例选择 small。
provider="cpu"
detectionProvider="cpu"
recognitionProvider="cpu"
detectionModel="/models/ocr/det.onnx"
recognitionModel="/models/ocr/rec.onnx"
dictionary="/models/ocr/dict.txt"
library="/opt/onnxruntime/lib/libonnxruntime.so.1"
warmupRuns=3
recognitionBatchSize=4
coremlCache=true # CPU 会话不创建 CoreML 缓存。
```

在仓库中准备模型，或使用已校验的离线模型包。目录必须尚不存在：

```bash
bash scripts/prepare-ocr-models.sh --size small /srv/inferkit/pp-ocrv6-small
```

模型权重与字典的 SHA256 可从生成的 `SHA256SUMS` 填入 OCR 配置，以固定实际部署文件。切换 medium/small 时必须成对更换模型，并同步更新旧的 pin；显式路径优先于 `modelSize` 预设。默认仍为 medium，不会在服务启动时自动下载模型。两档模型的识别差异见 [OCR 优化报告](OCR_OPTIMIZATION.md)。

```bash
cd /srv/inferkit
export INFERKIT_IMAGE=ghcr.io/example/inferkit:debian13
export INFERKIT_OCR_MODEL=./pp-ocrv6-small
docker compose pull
docker compose up -d
docker compose logs --tail=100 inferkit
```

`INFERKIT_OCR_MODEL` 也可指向准备好的 medium 目录。Compose 将 OCR 模型只读挂载至 `/models/ocr`，分类模型只读挂载至 `/models/artwork_fp32`。两者的 `library` 使用同一 `/opt/onnxruntime/lib/libonnxruntime.so.1`。

OCR 页面为 `/ocr`，鉴权 API 为 `POST /api/image/ocr`；文件上传、粘贴和 URL 沿用现有功能，详见 [OCR 文档](OCR.md)。首次加载日志应包含两个模型的实际 `execution_provider=cpu`、预热完成和自动资源计划；每次请求有 `OCR timing`。Debian CPU 不启用 CoreML；SIMD 按 CPU 能力选择，线程和并发继续自动计算。

同一镜像也可覆盖命令执行 OCR CLI，不启动 Redis：

```bash
docker compose run --rm --no-deps \
  -v /absolute/path/to/images:/input:ro inferkit \
  --config /etc/inferkit/app.toml ocr /input/page.png
```

## 复现镜像发布前验收

安装并启动 amd64 Docker Engine 后，先按上文构建镜像，再运行：

```bash
python3 scripts/test-inference-image.py inferkit:debian13
# 离线验收：使用已经准备并校验的 small 模型目录，跳过下载。
python3 scripts/test-inference-image.py inferkit:debian13 --models /absolute/path/to/pp-ocrv6-small
# Intel AVX2 服务器：同一模型对比 CPU/OpenVINO 的四种阶段组合。
python3 scripts/test-inference-image.py inferkit:debian13 --models /absolute/path/to/pp-ocrv6-small --with-openvino
```

脚本使用已存在的镜像，不自动拉取镜像；推理容器没有网络。默认只下载官方公开测试权重，不接触业务配置或私有训练模型。合成文字图位于 `services/ocr/testdata/ci.png`，不含用户图片。该验收确认部署功能和优化已接通，不构成业务准确率、Debian 性能或并发 SLO 结论。

## 在 Debian 11 宿主机使用 OpenVINO

宿主机运行 rootless Docker，容器内仍是 Debian 13；OpenVINO、TBB、hwloc 等用户态依赖全部在镜像 `/opt/openvino/lib` 中。CPU FP32 不需要在 Debian 11 宿主机安装 OpenVINO、Python 或 GPU 驱动。CPU 需要由虚拟机/宿主机暴露 Intel AVX2；Xeon Platinum 8163 提供的 AVX2/AVX-512 可用于测试，但不保证比默认 CPU EP 更快。

修改现有 `app.toml` 的对应字段，其余密钥、Redis、模型路径及 pin 继续保留：

```toml
[image.ocr]
enabled=true
modelSize="small" # 必须与实际挂载的配套 ONNX 文件一致。
provider="cpu"
detectionProvider="openvino"
recognitionProvider="openvino"
```

```bash
docker compose pull
docker compose up -d --force-recreate inferkit
docker compose logs --tail=100 inferkit
```

启动日志应包含 `precision=fp32` 的 OpenVINO 会话配置、实际阶段 provider 和预热完成。缺少插件、设备不支持或算子不能分配给 OpenVINO 时启动失败；可将任一阶段改回 `cpu` 对比或回滚。macOS 开发配置继续使用 `cpu` 或原有 `auto/coreml`，不需要安装 Linux 插件。

`scripts/prepare-openvino-linux.py` 下载官方 `onnxruntime-ep-openvino` 1.8.1 Linux amd64 wheel 并核对固定 SHA256，仅提取 CPU 所需 ELF 和随包声明，不在最终镜像中安装 Python。它不复制 GPU/NPU/AUTO 插件，也不覆盖系统库。`--archive` 可使用同一份校验过的 wheel 离线准备运行库。Docker 构建会用最终非 root 用户检查每个 OpenVINO 动态库的 `ldd` 依赖，插件只在显式选择时加载。[官方插件说明](https://github.com/onnxruntime/onnxruntime-ep-openvino/blob/main/docs/QuickStart.md)
