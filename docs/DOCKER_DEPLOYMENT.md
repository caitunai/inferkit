# Debian 13 amd64 Docker 部署

镜像基于官方 `debian:trixie-slim`（Debian 13），仅支持 `linux/amd64`。每次 Actions 构建会拉取最新基础镜像，跟进 Debian 13 的点版本；不使用可能在未来切换到 Debian 14 的 `debian:latest`。

Debian 13 是容器内的运行环境，宿主机可以继续使用 Debian 11；原生库使用容器内的 glibc，而非宿主机 glibc。宿主机需具备可用的 amd64 Docker Engine 和相应内核支持。

前端由 Node 24 构建，随后嵌入 Go 二进制；Go 使用 `golang:1.27-trixie`、CGO 和 `GOAMD64=v1` 编译。镜像包含固定的 ONNX Runtime CPU 1.31.1 及其系统依赖，复用 Debian 11 部署的官方包下载与 SHA-256 校验，以及共享的 C API 版本核对脚本。业务推理继续仅使用 CPU。

配置、训练模型、密钥和数据库内容通过外部配置或挂载提供。`.dockerignore` 使用构建输入白名单，排除 `.git`、本机 `.app.toml` / `.env`、模型权重、私钥、依赖目录和已有构建产物，避免它们进入镜像或 CI 构建缓存。

## GitHub Actions 构建和发布

工作流文件为 `.github/workflows/docker.yml`，名称是 **Debian 13 amd64 image**：

- 推送 `main` / `master`、推送 `v*` 标签时构建并发布；仓库默认分支更新 `latest`，版本标签生成同名镜像标签。
- 每次发布同时生成 `sha-<完整提交 SHA>` 标签。
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

工作流先构建并载入镜像，再检查 Debian 13、amd64、非 root 身份、程序动态依赖和 CLI 可执行性，最后推送**同一份已检查的镜像**，不会在测试后重新构建另一份。构建过程也会在实际运行镜像的基础环境中，以最终服务账户初始化 ONNX Runtime、创建 / 释放张量；测试二进制仅留在中间构建层。

镜像以 amd64 原生 runner 构建，不需要 QEMU。中间层通过 GitHub Actions cache 复用。首次构建包含 Go / npm 依赖、前端、测试及原生库下载，后续构建会复用缓存。

首次发布的 GHCR Package 可能为私有。部署机需要先 `docker login ghcr.io`，使用具备该 Package 读取权限的凭据；也可由仓库管理员在 GHCR 中调整 Package 的可见性。工作流使用的 `GITHUB_TOKEN` 不应复制到部署机配置中。

### 同时发布到 Docker Hub

在 GitHub 仓库 **Settings → Secrets and variables → Actions** 中添加：

| 类型 | 名称 | 内容 |
| --- | --- | --- |
| Repository variable | `DOCKERHUB_USERNAME` | Docker Hub 登录用户名；设置后启用 Docker Hub 发布 |
| Repository secret | `DOCKERHUB_TOKEN` | 对目标仓库具有推送权限的 Docker Hub Access Token |
| Repository variable，可选 | `DOCKERHUB_IMAGE` | `namespace/repository`，如 `my-org/inferkit`；默认 `<DOCKERHUB_USERNAME>/<GitHub仓库名>` |

提前创建相应 Docker Hub 仓库，并保证登录账户和 Token 有推送权限。组织仓库通过 `DOCKERHUB_IMAGE` 指定；该变量也接受 `docker.io/namespace/repository`，不附带标签。镜像名称统一转为小写，Docker Hub 和 GHCR 使用同样的 `latest`、版本和提交 SHA 标签。

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
└── artwork_fp32/      # 完整模型包
    ├── artwork.onnx
    ├── deployment.json
    ├── labels.json
    ├── validation.json
    └── fixtures/
```

现有配置应保留 API 鉴权的 `secret`、`audience`、`issuers` 和应用 `key`，以继续使用已有 Token。全新部署需要填写密钥；`key` 应为 32 个字符，`auth.api.secret` 至少 32 字节。示例文件故意不包含可直接使用的密钥。可分别使用 `openssl rand -hex 16`、`openssl rand -hex 32` 为新部署生成值。

修改模型和运行库为容器内路径：

```toml
[image.filter]
model="/models/artwork_fp32"
library="/opt/onnxruntime/lib/libonnxruntime.so.1"
```

运行账户 UID/GID 为 **10001:10001**。确保它能读取配置及模型，例如让配置归属 `root:10001`、权限 `0640`，模型目录允许该账户读取和进入。命名数据卷自动使用镜像中 `/var/lib/inferkit` 的服务账户权限。启用配置加密时，外部 keyring 也需单独只读挂载，并将配置中的路径改为容器内路径。

在部署目录执行，替换为实际的小写仓库名称：

```bash
cd /srv/inferkit
export INFERKIT_IMAGE=ghcr.io/example/inferkit:latest
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100 inferkit
```

也可以在部署目录的 `.env` 中设置 `INFERKIT_IMAGE`。Compose 要求显式提供镜像名称，并拒绝将缺失的配置文件自动创建成目录。可选变量：`INFERKIT_PORT`（宿主机端口，默认 8081）、`INFERKIT_CONFIG`（配置文件路径）、`INFERKIT_MODEL`（模型目录）。相对挂载路径以 Compose 文件所在目录为基准。

默认仅将 `127.0.0.1:8081` 映射到容器 `8080`，可以直接接入现有 Nginx 的 `proxy_pass http://127.0.0.1:8081`。页面和 API 路径保持原样：`/image-filter`、`/api/image/filter`、`/image-review`。继续传递 `Host` 和 `X-Forwarded-Proto`。

容器环境变量 `PORT` 默认是 `8080`，优先于 TOML 中的 `port`；Compose 固定保持该容器端口，`INFERKIT_PORT` 只改变宿主机映射。镜像健康检查访问图片工作台页面，验证服务已经启动，不重复初始化模型或执行推理。`docker compose ps` 可看到健康状态；Docker 的健康状态本身不会自动重启进程，当前重启策略在进程退出时生效。

容器根目录只读，配置和模型只读挂载，临时文件放在独立 `/tmp`，持久文件放在数据卷。默认进程执行 `image-filter serve`；需要完整服务时可在 Compose 中增加：

```yaml
command: ["--config", "/etc/inferkit/app.toml", "serve"]
```

完整服务需要现有 Redis / Queue 配置。启用识别结果采集和人工审核时继续配置 MySQL，并在更新服务前按 [IMAGE_REVIEW.md](IMAGE_REVIEW.md) 执行 Atlas 迁移。容器内的 `localhost` 指向当前容器，数据库 / Redis 地址应改为容器可达的服务名称或地址。

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

当前开发环境没有 Docker CLI / Engine，未在本机实际构建或推送镜像。首次完整镜像构建与运行检查会在工作流提交、推送到 GitHub 后执行。

实现参考：[Debian 13 发布信息](https://www.debian.org/releases/trixie/)、[Docker 构建后测试再推送](https://docs.docker.com/build/ci/github-actions/test-before-push/)、[GitHub 发布镜像到 GHCR](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)、[Docker Hub 与 GHCR 同时发布](https://docs.docker.com/build/ci/github-actions/push-multi-registries/)。
