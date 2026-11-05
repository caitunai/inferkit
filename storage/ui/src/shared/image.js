export function imageURL(value) {
  const raw = String(value || "").trim();
  if (!raw || raw.length > 8192) throw new Error("请输入有效的图片 URL。");
  if (!/^https?:\/\/[^/\\\s]/i.test(raw)) throw new Error("请输入完整的 http:// 或 https:// 图片地址。");
  let url;
  try { url = new URL(raw); } catch { throw new Error("请输入完整的 http:// 或 https:// 图片地址。"); }
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.hash) {
    throw new Error("图片地址须使用 HTTP(S)，且不包含用户名、密码或片段。");
  }
  return url.href;
}

export function percent(value) {
  return `${(Math.min(1, Math.max(0, Number(value) || 0)) * 100).toFixed(2)}%`;
}

const IMAGE_TYPES = new Set(["image/jpeg", "image/png", "image/webp", "image/avif"]);

export function supportedImageFile(file) {
  const type = String(file.type || "").toLowerCase();
  return IMAGE_TYPES.has(type) || ((!type || type === "application/octet-stream") && /\.(jpe?g|webp|avif|png)$/i.test(file.name || ""));
}
