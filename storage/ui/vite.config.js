import { fileURLToPath, URL } from "node:url";
import { writeFile } from "node:fs/promises";
import { dirname, relative, sep } from "node:path";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

const keepFileContent = "This file keeps the embedded UI dist directory present before assets are built.\n";
const viewsDirectory = fileURLToPath(new URL("../views", import.meta.url));
const configCenterDiffsThemeBundle = fileURLToPath(new URL("./src/config-center/diffs-theme-bundle.js", import.meta.url));
const configCenterShikiBundle = fileURLToPath(new URL("./src/config-center/shiki-bundle.js", import.meta.url));
const tailwindImportPattern = /@import\s+["']tailwindcss["'][^;]*;/;

function includeGoTemplateSources() {
  return {
    name: "include-go-template-sources",
    enforce: "pre",
    transform(source, id) {
      const stylesheet = id.split("?", 1)[0];
      if (!stylesheet.endsWith(".css") || !tailwindImportPattern.test(source)) {
        return null;
      }

      const sourcePath = relative(dirname(stylesheet), viewsDirectory).split(sep).join("/");
      return source.replace(
        tailwindImportPattern,
        (tailwindImport) => `${tailwindImport}\n@source "${sourcePath}";`,
      );
    },
  };
}

function preserveDistPlaceholder() {
  return {
    name: "preserve-dist-placeholder",
    closeBundle() {
      return writeFile(fileURLToPath(new URL("./dist/keep.txt", import.meta.url)), keepFileContent);
    },
  };
}

export default defineConfig({
  plugins: [includeGoTemplateSources(), tailwindcss(), preserveDistPlaceholder()],
  resolve: {
    alias: [
      { find: /^@pierre\/theming\/themes$/, replacement: configCenterDiffsThemeBundle },
      { find: /^shiki$/, replacement: configCenterShikiBundle },
    ],
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    manifest: "manifest.json",
    rollupOptions: {
      input: {
        configCenter: fileURLToPath(new URL("./src/config-center/main.js", import.meta.url)),
      },
    },
  },
});
