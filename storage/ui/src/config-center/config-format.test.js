import assert from "node:assert/strict";
import test from "node:test";

import {
  formatConfig,
  mergeConfigState,
  moveArrayDescriptionItem,
  pathToPointer,
  removeArrayDescriptionItem,
  remapDescriptionPrefix,
} from "./config-format.js";

test("description paths use RFC 6901 escaping and follow renamed keys", () => {
  assert.equal(pathToPointer(["service/api", "a~b", 0]), "/service~1api/a~0b/0");
  assert.deepEqual(
    remapDescriptionPrefix({ "/old": "group", "/old/name": "name", "/keep": "keep" }, ["old"], ["new"]),
    { "/new": "group", "/new/name": "name", "/keep": "keep" },
  );
});

test("array descriptions follow move and delete operations", () => {
  const descriptions = { "/items/0": "first", "/items/0/name": "first name", "/items/1": "second" };
  assert.deepEqual(moveArrayDescriptionItem(descriptions, ["items"], 0, 1), {
    "/items/1": "first",
    "/items/1/name": "first name",
    "/items/0": "second",
  });
  assert.deepEqual(removeArrayDescriptionItem(descriptions, ["items"], 0, 2), {
    "/items/0": "second",
  });
});

test("mergeConfigState replaces descriptions with arrays and scalars", () => {
  const merged = mergeConfigState(
    { service: { host: "base", port: 80 }, regions: ["cn", "us"] },
    { "/service": "service", "/service/host": "base host", "/regions/1": "US" },
    { service: { host: "prod" }, regions: ["cn"] },
    { "/service/host": "production host", "/regions": "allowed regions", "/regions/0": "China" },
  );
  assert.deepEqual(merged.config, { service: { host: "prod", port: 80 }, regions: ["cn"] });
  assert.deepEqual(merged.descriptions, {
    "/service": "service",
    "/service/host": "production host",
    "/regions": "allowed regions",
    "/regions/0": "China",
  });
});

test("formatConfig emits every supported format with descriptions", () => {
  const config = { service: { host: "localhost", port: 8080 }, enabled: true, regions: ["cn", "us"] };
  const descriptions = { "/service": "服务配置", "/service/host": "监听地址", "/regions/0": "中国区域" };

  const json = JSON.parse(formatConfig(config, descriptions, "json"));
  assert.deepEqual(json, { config, descriptions });
  assert.match(formatConfig(config, descriptions, "yaml"), /# 监听地址\n  host: "localhost"/);
  assert.match(formatConfig(config, descriptions, "toml"), /# \/service\/host: 监听地址/);
  assert.match(formatConfig(config, descriptions, "env"), /SERVICE__HOST="localhost"/);
  assert.match(formatConfig(config, descriptions, "ini"), /\[service\]\nhost="localhost"/);
});
