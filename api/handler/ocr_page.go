package handler

import "github.com/caitun/inferkit/api/base"

const ocrEntry = "src/ocr/main.js"

// OCRPage serves the OCR workbench; recognition uses the authenticated API.
func OCRPage(service TextRecognizer) base.HandlerFunc {
	maxBytes, timeout, _ := service.Limits()
	return func(c *base.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		data := pageData(c, ocrEntry, "OCR 识别 · InferKit")
		data["max_bytes"], data["timeout_ms"] = maxBytes, timeout.Milliseconds()
		c.View("ocr.index", data)
	}
}
