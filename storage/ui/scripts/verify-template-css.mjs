import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const entry = "src/config-center/main.js";
const manifestPath = resolve("dist/manifest.json");
const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
const cssFiles = manifest[entry]?.css ?? [];

if (cssFiles.length === 0) {
  throw new Error(`Vite manifest does not contain CSS for ${entry}`);
}

const generatedCSS = (
  await Promise.all(cssFiles.map((file) => readFile(resolve("dist", file), "utf8")))
).join("\n");

const requiredSelectors = new Map([
  ["min-h-screen", ".min-h-screen{"],
  ["grid-cols-1", ".grid-cols-1{"],
  ["sm:inline", ".sm\\:inline{"],
  ["lg:grid layout", ".lg\\:grid-cols-\\[300px_minmax\\(0\\,1fr\\)\\]"],
  ["arbitrary page width", ".max-w-\\[1600px\\]"],
]);
const missing = [...requiredSelectors].filter(([, selector]) => !generatedCSS.includes(selector));

if (missing.length > 0) {
  throw new Error(
    `Tailwind did not scan storage/views; missing generated selectors: ${missing.map(([name]) => name).join(", ")}`,
  );
}

console.log(`Verified ${requiredSelectors.size} template-only Tailwind selectors.`);

const imageEntry = manifest["src/image-filter/main.js"];
if (!imageEntry?.css?.length) throw new Error("Image filter entry is missing its stylesheet");
const imageCSS = (await Promise.all(imageEntry.css.map((file) => readFile(resolve("dist", file), "utf8")))).join("\n");
for (const selector of [".grid-cols-1{", ".lg\\:grid-cols-2{", ".gap-6{", ".sr-only{", ".sm\\:grid-cols-2{", ".xl\\:grid-cols-4{"]) {
  if (!imageCSS.includes(selector)) throw new Error(`Image filter CSS is missing template utility ${selector}`);
}
console.log("Verified image filter layout and accessibility utilities.");

const reviewEntry = manifest["src/image-review/main.js"];
if (!reviewEntry?.css?.length) throw new Error("Image review entry is missing its stylesheet");
const reviewCSS = (await Promise.all(reviewEntry.css.map((file) => readFile(resolve("dist", file), "utf8")))).join("\n");
for (const selector of [".grid-cols-1{", ".lg\\:grid-cols-2{", ".sm\\:grid-cols-2{", ".xl\\:grid-cols-4{", ".review-single", ".review-filters", ".review-stage", ".review-labels", ".review-choice", ".review-model", ".review-modal::backdrop", ".review-card-actions", ".review-selected", ".review-shortcut", ".review-adopt"]) {
  if (!reviewCSS.includes(selector)) throw new Error(`Image review CSS is missing ${selector}`);
}
const cardActionsRule = reviewCSS.match(/\.review-card-actions\{([^}]+)\}/)?.[1];
if (!cardActionsRule?.includes("display:grid") || !cardActionsRule.includes("grid-template-columns:repeat(3,")) throw new Error("Review cards must provide a compact three-column classification grid");
const reviewLabelsRule = reviewCSS.match(/\.review-labels\{([^}]+)\}/)?.[1];
if (!reviewLabelsRule?.includes("grid-template-columns:repeat(3,")) throw new Error("Single-image and modal classification controls must use three columns");
console.log("Verified image review gallery and single-image annotation styles.");
const toastRule = reviewCSS.match(/\.review-toast-region\{([^}]+)\}/)?.[1];
if (!toastRule?.includes("position:fixed")) throw new Error("Image review toast must stay outside the page layout");
console.log("Verified image review toast uses fixed positioning.");

const ocrEntry = manifest["src/ocr/main.js"];
if (!ocrEntry?.css?.length) throw new Error("OCR entry is missing its stylesheet");
const ocrCSS = (await Promise.all(ocrEntry.css.map((file) => readFile(resolve("dist", file), "utf8")))).join("\n");
for (const selector of [".grid-cols-1{", ".lg\\:grid-cols-2{", ".sm\\:grid-cols-2{", ".xl\\:grid-cols-4{", ".sr-only{", ".ocr-result-preview", ".ocr-lines", ".ocr-text", ".ik-navigation"]) {
  if (!ocrCSS.includes(selector)) throw new Error(`OCR CSS is missing ${selector}`);
}
for (const entry of ["src/config-center/main.js", "src/image-filter/main.js", "src/image-review/main.js"]) {
  const css = (await Promise.all(manifest[entry].css.map((file) => readFile(resolve("dist", file), "utf8")))).join("\n");
  if (!css.includes(".ik-navigation")) throw new Error(`Shared navigation CSS is missing for ${entry}`);
}
console.log("Verified OCR utilities and shared feature navigation.");
