export const DETECTION_ROUTES = Object.freeze([
  { id: "default", label: "默认入口 · libvips", path: "/api/image/filter" },
  { id: "libvips", label: "libvips · 原生处理", path: "/api/image/filter/libvips" },
  { id: "go", label: "Go＋archsimd", path: "/api/image/filter/go" },
].map(Object.freeze));

export function detectionRoute(id = "default") {
  const route = DETECTION_ROUTES.find((item) => item.id === id);
  if (!route) throw new Error("请选择支持的检测路径。");
  return route;
}
