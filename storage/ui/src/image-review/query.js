const sorts = new Set(["newest", "oldest", "confidence_asc", "confidence_desc"]);
const reviews = new Set(["all", "pending", "reviewed"]);
export const reviewPageSize = 24;
export const maxReviewOffset = 20_000;
export const maxReviewJumpPage = Math.floor(maxReviewOffset / reviewPageSize) + 1;

export function offsetForPage(value) {
  const text = String(value).trim(), page = Number(text);
  if (!/^[1-9]\d*$/.test(text) || !Number.isSafeInteger(page) || page > maxReviewJumpPage) {
    throw new Error(`页码跳转范围为 1–${maxReviewJumpPage}，offset 不能超过 ${maxReviewOffset}；更深记录请使用下一页。`);
  }
  return (page - 1) * reviewPageSize;
}

export function filtersFromURL(search, review) {
  const params = new URLSearchParams(search);
  const correction = params.get("correction") || "";
  return {
    label: params.get("label") || "", correction, min: params.get("min") ?? "0", max: params.get("max") ?? "100",
    review: correction ? "reviewed" : params.get("review") || (review ? "pending" : "all"), sort: params.get("sort") || "newest",
  };
}

export function queryParams(filters, cursor = null, limit = reviewPageSize, offset = 0) {
  const min = Number(filters.min), max = Number(filters.max);
  const correction = filters.correction || "";
  if (!Number.isFinite(min) || !Number.isFinite(max) || min < 0 || max > 100 || min > max) throw new Error("概率范围须在 0–100% 之间，且下限不大于上限。");
  if (!sorts.has(filters.sort) || !reviews.has(filters.review) || filters.label.length > 128 || typeof correction !== "string" || correction.length > 128) throw new Error("筛选条件不合法。");
  if (!Number.isSafeInteger(offset) || offset < 0 || offset > maxReviewOffset || (cursor && offset !== 0)) throw new Error("分页 offset 不合法，或与游标同时使用。");
  const params = new URLSearchParams({ label: filters.label, review: filters.review, sort: filters.sort,
    min_confidence: String(min / 100), max_confidence: String(max / 100), limit: String(limit) });
  if (correction) params.set("correction", correction);
  if (cursor) { params.set("after_id", String(cursor.id)); params.set("after_confidence", String(cursor.confidence)); }
  if (offset > 0) params.set("offset", String(offset));
  return params;
}

export function cursorOf(record) { return record ? { id: record.id, confidence: record.confidence } : null; }

export function reviewHref(filters, id = null) {
  const params = new URLSearchParams(filters);
  if (id !== null) params.set("id", String(id));
  return `/image-review/review?${params}`;
}

export function safeImageURL(value) {
  try {
    const url = new URL(value);
    return ["http:", "https:"].includes(url.protocol) && !url.username && !url.password ? url.href : "";
  } catch { return ""; }
}
