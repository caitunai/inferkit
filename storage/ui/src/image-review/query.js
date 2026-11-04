const sorts = new Set(["newest", "oldest", "confidence_asc", "confidence_desc"]);
const reviews = new Set(["all", "pending", "reviewed"]);

export function filtersFromURL(search, review) {
  const params = new URLSearchParams(search);
  return {
    label: params.get("label") || "", min: params.get("min") ?? "0", max: params.get("max") ?? "100",
    review: params.get("review") || (review ? "pending" : "all"), sort: params.get("sort") || "newest",
  };
}

export function queryParams(filters, cursor = null, limit = 24) {
  const min = Number(filters.min), max = Number(filters.max);
  if (!Number.isFinite(min) || !Number.isFinite(max) || min < 0 || max > 100 || min > max) throw new Error("概率范围须在 0–100% 之间，且下限不大于上限。");
  if (!sorts.has(filters.sort) || !reviews.has(filters.review) || filters.label.length > 128) throw new Error("筛选条件不合法。");
  const params = new URLSearchParams({ label: filters.label, review: filters.review, sort: filters.sort,
    min_confidence: String(min / 100), max_confidence: String(max / 100), limit: String(limit) });
  if (cursor) { params.set("after_id", String(cursor.id)); params.set("after_confidence", String(cursor.confidence)); }
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
