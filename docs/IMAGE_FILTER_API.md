# 图片分类 API 接入文档

本文面向第三方调用者。服务端安装、模型配置和运维说明见 [Go CPU 图片过滤部署](GO_ONNX_SERVER.md)。

## 接口与鉴权

```text
POST {BASE_URL}/api/image/filter
Authorization: Bearer {API_TOKEN}
```

服务默认同时提供两条图片处理路径，无需切换服务配置：

| 路径 | 图片处理后端 |
| --- | --- |
| `/api/image/filter` | libvips，保留原有默认行为 |
| `/api/image/filter/libvips` | 显式 libvips |
| `/api/image/filter/go` | Go 解码与 Lanczos，按构建及 CPU 能力使用 archsimd |

以上接口均使用 POST，鉴权、请求体与响应结构一致。调用方仅需替换路径即可对比；两条路径共享模型和总资源预算，但分别缓存结果。Go 路径与 libvips 训练预处理存在数值差异，类别及概率应一并评估，详见[后端对比](IMAGE_PREPROCESS_BACKENDS.md)。

`BASE_URL` 和 `API_TOKEN` 由服务提供方交付，生产环境应使用 HTTPS。每次请求检测一张图片，支持上传文件或提供图片 URL，返回分类和各类别概率。

Token 由服务管理员在服务器上签发，例如：

```bash
go run . --config .app.toml api-token create --subject partner-image-client
```

默认有效期为一年（365 天），管理员可通过 `--expires-in 24h` 等参数调整。调用方只需要 Token，不需要服务端的签名密钥或模型文件。Token 过期后请联系服务提供方重新签发；当前没有通过 HTTP 签发或刷新 Token 的接口。请通过请求头传递 Token，勿放入 URL 查询参数。

以下 cURL 示例使用环境变量，先替换为服务方提供的地址及 Token：

```bash
export BASE_URL='https://your-api.example.com'
export API_TOKEN='replace-with-issued-token'
```

## 请求方式

### 上传图片

使用 `multipart/form-data`，文件字段名为 `file`：

```bash
curl --fail-with-body "${BASE_URL}/api/image/filter" \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -F 'file=@/path/to/image.avif'
```

### 检测图片 URL

使用 `application/json`，字段名为 `url`，服务端下载图片后进行检测：

```bash
curl --fail-with-body "${BASE_URL}/api/image/filter" \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -H 'Content-Type: application/json' \
  --data '{"url":"https://example.com/image.webp"}'
```

也支持 `application/x-www-form-urlencoded` 的 `url` 字段：

```bash
curl --fail-with-body "${BASE_URL}/api/image/filter" \
  -H "Authorization: Bearer ${API_TOKEN}" \
  --data-urlencode 'url=https://example.com/image.png'
```

仅传 URL 时也可使用 multipart 的 `url` 字段。

### 上传图片并附带来源 URL

在同一个 multipart 请求中提交 `file` 和 `url`：

```bash
curl --fail-with-body "${BASE_URL}/api/image/filter" \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -F 'file=@/path/to/image.jpeg' \
  -F 'url=https://example.com/original.jpeg'
```

服务端**仅检测上传文件**，`url` 作为该文件的来源标识返回，不会再次下载，也不会校验上传文件是否与远端内容一致。此时 URL 仍须符合下文的 URL 语法要求。

服务方启用结果采集时，带 URL 的成功检测会同时保存来源、模型预测及图片内容哈希，用于后续人工复核。未提供 URL 的上传不进入该数据表。只有持久化成功才返回检测成功；存储失败返回 HTTP 503，可有限次数退避重试，重复请求不会覆盖人工标注或新增相同样本。

### 参数说明

| 参数 | 类型 | 必填条件 | 说明 |
| --- | --- | --- | --- |
| `file` | 二进制文件 | 未提供 `url` 时必填 | 仅支持 multipart，每次最多一个文件 |
| `url` | 字符串 | 未提供 `file` 时必填 | HTTP(S) 图片地址；附带文件时作为来源标识 |

不支持在 JSON 中提交 Base64 图片、服务器本地文件路径或图片数组。multipart 请求请让 cURL、浏览器或 HTTP 客户端自动生成 `Content-Type` 和 boundary。

## 图片与 URL 限制

- 支持 JPEG（`.jpg` / `.jpeg`）、WebP、AVIF 和 PNG，按实际图片内容识别格式，不依赖文件名或上传声明的 MIME 类型。
- 仅进行单张静态图分类，不提供动画或视频的逐帧检测结果。图片方向修正、透明区域白底合成和模型缩放由服务端完成，调用方可直接上传原始图片。
- URL 必须使用 `http://` 或 `https://`，包含有效主机名，最长 8192 字节，不允许用户名、密码或 `#fragment`。
- 仅 URL 检测需要地址可被服务端直接访问，无需额外登录或自定义下载请求头。使用服务器的系统 DNS 和网络连接，不限制目标 IP 网段；最多跟随三次重定向，每次跳转均校验 HTTP(S) URL 格式。

默认部署限制如下，服务提供方可调整配置，请以其实际约定为准：

| 限制 | 默认值 |
| --- | --- |
| 单张图片编码后的大小 | 10 MiB，即 10485760 字节 |
| 图片总像素数 | 20000000 像素 |
| 单边尺寸上限 | 32768 像素 |
| HTTP 请求体上限 | 图片大小上限额外加 64 KiB，用于 multipart 等元数据 |
| 单次处理时限 | 30 秒，包含上传、下载、预处理及推理 |
| 每个服务实例同时接收的请求数 | 8，超出返回 HTTP 429 |

URL 下载的图片与上传文件适用相同的图片大小和尺寸限制。调用方应设置有限的请求超时，并留出必要的网络传输余量。

## 成功响应

HTTP 状态为 `200`，业务字段 `status` 为 `0`。结果位于 `data.result`：

```json
{
  "status": 0,
  "message": "ok",
  "data": {
    "result": {
      "url": "https://example.com/original.jpeg",
      "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "label": "1_valid",
      "labels": ["0_invalid", "1_valid"],
      "probabilities": [0.03, 0.97],
      "class_id": 1,
      "confidence": 0.97
    }
  }
}
```

以上哈希和概率为格式示例，实际数值取决于图片及部署模型。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `url` | string，可省略 | 调用方提供的原始 URL；未传 URL 时不返回该字段 |
| `sha256` | string | 实际上传或下载的完整原始图片字节的 SHA-256，64 位小写十六进制；在解码和缩放之前计算 |
| `label` | string | 概率最大的类别名称，等于 `labels[class_id]` |
| `labels` | string[] | 当前模型的类别顺序 |
| `probabilities` | number[] | 与 `labels` 逐项对应的概率，取值为 0 到 1，总和允许浮点误差 |
| `class_id` | integer | 最大概率类别的索引，从 0 开始 |
| `confidence` | number | 最大概率，即 `probabilities[class_id]` |

当前 artwork 模型包含 `0_invalid`（无效类）和 `1_valid`（有效类）。客户端应根据响应中的标签对应概率，不要自行重排类别或再次计算 Softmax。服务只返回模型分类，不设置额外业务阈值；业务方可依据概率决定通过、拒绝或人工复核。

`sha256` 基于原始文件字节，不基于 URL、文件名或缩放后的像素。内容完全一致的文件具有相同哈希；重新编码、修改元数据等操作可能改变哈希，即使图片看起来相同。

## 错误响应与重试

接口处理的错误使用对应 HTTP 状态码，JSON 中的 `status` 同为该状态码，例如：

```json
{
  "status": 400,
  "message": "invalid image or URL",
  "data": {}
}
```

| HTTP 状态 | 含义 | 调用方处理 |
| --- | --- | --- |
| `400` | 缺少图片、图片损坏、格式不支持或 URL 不合法 | 修正输入；同一输入不要直接重复请求 |
| `403` | Token 缺失、无效、过期或 API 身份不符合配置 | 检查 Authorization 请求头，联系服务方更新凭据 |
| `408` | 请求取消或处理超时 | 检查网络及图片大小，必要时有限次数重试 |
| `413` | 图片字节数、像素数、单边尺寸或请求体超过限制 | 缩小或压缩图片后重新提交 |
| `429` | 服务接收的请求已满 | 降低并发，退避后重试 |
| `500` | 模型推理失败或输出异常 | 保留发生时间及输入标识，联系服务方排查 |
| `502` | 图片下载失败，或远端未返回 HTTP 200 | 检查图片地址及远端可用性，必要时改为上传文件 |
| `503` | 模型服务正在关闭，或启用的结果存储不可用 | 稍后重试或联系服务方 |

程序应判断 HTTP 状态及 `status`，不要依赖 `message` 的具体文字。分类结果为 `0_invalid` 时仍是一次成功检测，返回 HTTP 200，不属于接口错误。

图片域名解析失败时，返回 HTTP 502，并提供 `data.code: "image_url_dns_failed"`。其他错误的 `data` 可以为空对象。下载超时（含连接、响应头和响应体读取）返回 HTTP 408；下载总时限由服务端的 `image.filter.timeout` 配置，默认 30 秒。

对可恢复的错误使用带随机抖动的指数退避并限制次数，例如最多重试三次；不要立即无限重试。接口没有需要调用方提供的幂等键或固定的 `Retry-After` 响应头。反向代理等基础设施也可能返回非 JSON 错误，客户端需保留对此类响应的处理。

## JavaScript 调用示例

以下函数适用于浏览器的 `fetch`，`file` 可直接使用文件选择器、粘贴或拖放获取的 `File`。不传文件时使用 JSON 提交 URL；传文件时使用 multipart，可同时附带来源 URL。

```javascript
async function filterImage({ baseURL, token, file, url, signal }) {
  const headers = { Authorization: `Bearer ${token}` };
  let body;
  if (file) {
    body = new FormData();
    body.append('file', file);
    if (url) body.append('url', url);
  } else {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify({ url });
  }

  const response = await fetch(`${baseURL.replace(/\/$/, '')}/api/image/filter`, {
    method: 'POST', headers, body, signal,
  });
  const text = await response.text();
  let payload;
  try {
    payload = JSON.parse(text);
  } catch {
    throw new Error(`图片接口返回非 JSON 响应（HTTP ${response.status}）`);
  }
  if (!response.ok || payload.status !== 0) {
    throw new Error(`图片检测失败（HTTP ${response.status}）：${payload.message ?? 'unknown error'}`);
  }
  return payload.data.result;
}

const result = await filterImage({
  baseURL: 'https://your-api.example.com',
  token: 'replace-with-issued-token',
  url: 'https://example.com/image.png',
  signal: AbortSignal.timeout(35000), // 按服务端时限和网络情况调整。
});
console.log(result.label, result.confidence);
```

浏览器跨域调用需由服务方允许对应 Origin 及 Authorization 请求头；服务间调用不受浏览器 CORS 限制。已有浏览器检测工作台位于 `{BASE_URL}/image-filter`，便于人工验证 Token 和图片输入。
