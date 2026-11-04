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
