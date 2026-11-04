import assert from "node:assert/strict";
import test from "node:test";

import {
  buildComparisonSourceRows,
  buildEnvironmentTree,
  buildEnvironmentDisplayTree,
  buildParentEnvironmentTree,
  cloneConfigValue,
  ConfigTypeMemory,
  configValueType,
  defaultConfigValue,
  defaultComparisonSources,
  deepMerge,
  formatEnvironmentTreeLabel,
} from "./config-value.js";

test("cloneConfigValue clones Alpine-style proxies", () => {
  const reactiveConfig = new Proxy({
    service: new Proxy({ host: "base", port: 8080 }, {}),
    regions: new Proxy(["cn", "us"], {}),
    enabled: true,
  }, {});

  const cloned = cloneConfigValue(reactiveConfig);

  assert.deepEqual(cloned, {
    service: { host: "base", port: 8080 },
    regions: ["cn", "us"],
    enabled: true,
  });
  assert.notEqual(cloned, reactiveConfig);
  assert.notEqual(cloned.service, reactiveConfig.service);
  assert.notEqual(cloned.regions, reactiveConfig.regions);
});

test("deepMerge accepts proxies and replaces arrays and scalar values", () => {
  const inherited = new Proxy({
    service: { host: "base", port: 8080 },
    regions: ["cn", "us"],
    enabled: true,
  }, {});
  const draft = new Proxy({
    service: new Proxy({ host: "production" }, {}),
    regions: new Proxy(["cn"], {}),
    enabled: false,
  }, {});

  assert.deepEqual(deepMerge(inherited, draft), {
    service: { host: "production", port: 8080 },
    regions: ["cn"],
    enabled: false,
  });
});

test("buildEnvironmentTree describes branch depth and continuation lines", () => {
  const rows = buildEnvironmentTree([
    { id: 4, parent_id: 1, name: "qa", slug: "qa" },
    { id: 3, parent_id: 2, name: "production", slug: "prod" },
    { id: 1, parent_id: 0, name: "base", slug: "base" },
    { id: 2, parent_id: 1, name: "development", slug: "dev" },
    { id: 5, parent_id: 0, name: "standalone", slug: "standalone" },
  ]);

  assert.deepEqual(
    rows.map((row) => ({
      id: row.environment.id,
      depth: row.depth,
      guides: row.ancestorContinuations,
      isLast: row.isLast,
      hasChildren: row.hasChildren,
      hasPrevious: row.hasPrevious,
    })),
    [
      { id: 1, depth: 0, guides: [], isLast: false, hasChildren: true, hasPrevious: false },
      { id: 2, depth: 1, guides: [true], isLast: false, hasChildren: true, hasPrevious: true },
      { id: 3, depth: 2, guides: [true, true], isLast: true, hasChildren: false, hasPrevious: true },
      { id: 4, depth: 1, guides: [true], isLast: true, hasChildren: false, hasPrevious: true },
      { id: 5, depth: 0, guides: [], isLast: true, hasChildren: false, hasPrevious: true },
    ],
  );

  assert.deepEqual(rows.map(formatEnvironmentTreeLabel), [
    "● base（base）",
    "├─● development（dev）",
    "│ └─● production（prod）",
    "└─● qa（qa）",
    "● standalone（standalone）",
  ]);
});

test("ConfigTypeMemory restores the last value used by each type", () => {
  const memory = new ConfigTypeMemory();
  const path = ["service", "limits"];
  const objectValue = { retries: 3, regions: ["cn", "us"] };

  memory.remember(path, "object", objectValue);
  memory.remember(path, "string", "temporary override");

  assert.deepEqual(memory.restore(path, "object", {}), objectValue);
  assert.equal(memory.restore(path, "string", ""), "temporary override");
  assert.equal(memory.restore(path, "bool", false), false);

  const restoredObject = memory.restore(path, "object", {});
  restoredObject.regions.push("eu");
  assert.deepEqual(memory.restore(path, "object", {}), objectValue);

  memory.clear();
  assert.deepEqual(memory.restore(path, "object", {}), {});
});

test("ConfigTypeMemory keeps long text as an editor type while storing a string", () => {
  const memory = new ConfigTypeMemory();
  const path = ["service", "template"];

  assert.equal(memory.resolve(path, "one line"), "string");
  assert.equal(memory.resolve(path, "first line\nsecond line"), "long_text");

  memory.select(path, "long_text");
  memory.remember(path, "long_text", "editable one-line text");
  assert.equal(memory.resolve(path, "editable one-line text"), "long_text");
  assert.equal(memory.restore(path, "long_text", ""), "editable one-line text");

  memory.select(path, "string");
  assert.equal(memory.resolve(path, "plain text"), "string");
  assert.equal(configValueType(defaultConfigValue("long_text")), "string");

  memory.clear();
  assert.equal(memory.resolve(path, "plain text"), "string");
});

test("buildEnvironmentDisplayTree adds a read-only root above real environments", () => {
  const rows = buildEnvironmentDisplayTree([
    { id: 1, parent_id: 0, name: "base", slug: "base" },
    { id: 2, parent_id: 1, name: "development", slug: "dev" },
    { id: 3, parent_id: 0, name: "production", slug: "prod" },
  ]);

  assert.deepEqual(
    rows.map((row) => ({
      id: row.environment.id,
      depth: row.depth,
      guides: row.ancestorContinuations,
      virtual: row.virtual ?? false,
    })),
    [
      { id: "config-root", depth: 0, guides: [], virtual: true },
      { id: 1, depth: 1, guides: [false], virtual: false },
      { id: 2, depth: 2, guides: [false, true], virtual: false },
      { id: 3, depth: 1, guides: [false], virtual: false },
    ],
  );
});

test("buildParentEnvironmentTree preserves every selector depth and always includes its root", () => {
  const rows = buildParentEnvironmentTree([
    { id: 4, parent_id: 3, name: "level-4", slug: "level-4" },
    { id: 2, parent_id: 1, name: "level-2", slug: "level-2" },
    { id: 1, parent_id: 0, name: "level-1", slug: "level-1" },
    { id: 3, parent_id: 2, name: "level-3", slug: "level-3" },
  ]);

  assert.deepEqual(rows.map((row) => [row.environment.id, row.depth]), [
    ["config-root", 0],
    [1, 1],
    [2, 2],
    [3, 3],
    [4, 4],
  ]);
  assert.equal(buildParentEnvironmentTree([])[0].virtual, true);
});

test("buildComparisonSourceRows keeps environment hierarchy and nests versions", () => {
  const rows = buildComparisonSourceRows([
    { id: 2, parent_id: 1, name: "development", slug: "dev" },
    { id: 3, parent_id: 0, name: "production", slug: "prod" },
    { id: 1, parent_id: 0, name: "base", slug: "base" },
  ], [
    { environment_id: 1, version: 1 },
    { environment_id: 2, version: 1 },
    { environment_id: 1, version: 2 },
  ]);

  assert.deepEqual(
    rows.map((row) => ({
      id: row.environment.id,
      depth: row.depth,
      sources: row.sources.map((source) => source.value),
    })),
    [
      { id: "config-root", depth: 0, sources: [] },
      { id: 1, depth: 1, sources: ["draft:1", "release:1:2", "release:1:1"] },
      { id: 2, depth: 2, sources: ["draft:2", "release:2:1"] },
      { id: 3, depth: 1, sources: ["draft:3"] },
    ],
  );
  assert.equal(rows[1].sources[0].fullLabel, "base（base） · 当前草稿");
});

test("defaultComparisonSources compares a child draft with its parent draft", () => {
  const environments = [
    { id: 1, parent_id: 0, name: "base", slug: "base" },
    { id: 2, parent_id: 1, name: "development", slug: "dev" },
  ];

  assert.deepEqual(defaultComparisonSources(environments, 2, []), {
    left: "draft:1",
    right: "draft:2",
  });
});

test("defaultComparisonSources uses latest releases when parent or child has no draft", () => {
  const environments = [
    { id: 1, parent_id: 0, name: "base", slug: "base", has_draft: false },
    { id: 2, parent_id: 1, name: "development", slug: "dev", has_draft: true },
  ];
  const releases = [
    { environment_id: 1, version: 1 },
    { environment_id: 1, version: 3 },
    { environment_id: 2, version: 2 },
  ];

  assert.deepEqual(defaultComparisonSources(environments, 2, releases), {
    left: "release:1:3",
    right: "draft:2",
  });

  environments[1].has_draft = false;
  assert.deepEqual(defaultComparisonSources(environments, 2, releases), {
    left: "release:1:3",
    right: "release:2:2",
  });
});

test("defaultComparisonSources compares a root draft with its first sibling", () => {
  const environments = [
    { id: 1, parent_id: 0, name: "z-current", slug: "current" },
    { id: 2, parent_id: 0, name: "a-first", slug: "first" },
    { id: 3, parent_id: 0, name: "b-second", slug: "second" },
  ];

  assert.deepEqual(defaultComparisonSources(environments, 1, []), {
    left: "draft:2",
    right: "draft:1",
  });
});

test("defaultComparisonSources uses the latest release for an isolated environment without a draft", () => {
  const environments = [{ id: 1, parent_id: 0, name: "only", slug: "only", has_draft: false }];
  const releases = [
    { environment_id: 1, version: 1 },
    { environment_id: 1, version: 3 },
    { environment_id: 1, version: 2 },
  ];

  assert.deepEqual(defaultComparisonSources(environments, 1, releases), {
    left: "release:1:3",
    right: "release:1:3",
  });
  assert.deepEqual(defaultComparisonSources(environments, 1, []), {
    left: "draft:1",
    right: "draft:1",
  });
});

test("buildComparisonSourceRows hides a draft source when the environment is fully published", () => {
  const rows = buildComparisonSourceRows([
    { id: 1, parent_id: 0, name: "base", slug: "base", has_draft: false },
  ], [
    { environment_id: 1, version: 2 },
    { environment_id: 1, version: 1 },
  ]);

  assert.deepEqual(rows[1].sources.map((source) => source.value), [
    "release:1:2",
    "release:1:1",
  ]);
});
