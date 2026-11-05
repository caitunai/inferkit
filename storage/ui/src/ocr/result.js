const MAX_TEXT_LENGTH = 2_000_000;

export function readOCRResult(value) {
  if (!value || typeof value.text !== "string" || value.text.length > MAX_TEXT_LENGTH
    || !Number.isInteger(value.width) || value.width < 1 || value.width > 32768
    || !Number.isInteger(value.height) || value.height < 1 || value.height > 32768
    || !Array.isArray(value.lines) || value.lines.length > 3000) {
    throw new Error("服务返回的 OCR 结果不完整，请重试。");
  }
  let length = 0;
  const lines = value.lines.map((line) => {
    if (!line || typeof line.text !== "string" || !Number.isFinite(line.confidence)
      || line.confidence < 0 || line.confidence > 1 || !Array.isArray(line.box) || line.box.length !== 4) {
      throw new Error("服务返回的文字区域无效，请重试。");
    }
    length += line.text.length;
    if (length > MAX_TEXT_LENGTH) throw new Error("服务返回的文字内容过长。");
    const box = line.box.map((point) => {
      if (!point || !Number.isFinite(point.x) || !Number.isFinite(point.y)
        || point.x < 0 || point.y < 0 || point.x > value.width || point.y > value.height) {
        throw new Error("服务返回的文字坐标无效，请重试。");
      }
      return { x: point.x, y: point.y };
    });
    return { text: line.text, confidence: line.confidence, box };
  });
  return { text: value.text, width: value.width, height: value.height, lines };
}

export function entryWeight(entry) {
  const textBytes = 2 * ((entry.result?.text.length || 0)
    + (entry.result?.lines.reduce((sum, line) => sum + line.text.length, 0) || 0));
  return textBytes + (entry.result?.lines.length || 0) * 128 + (entry.previewBytes || 0) + 2048;
}
