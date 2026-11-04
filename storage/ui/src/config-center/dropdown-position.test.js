import assert from "node:assert/strict";
import test from "node:test";
import { calculateDropdownPosition } from "./dropdown-position.js";

test("opens below when the type selector has enough viewport space", () => {
  assert.deepEqual(calculateDropdownPosition({
    triggerTop: 100,
    triggerBottom: 136,
    viewportHeight: 800,
    menuHeight: 200,
  }), {
    opensAbove: false,
    maxHeight: 224,
  });
});

test("opens above when a type selector is close to the viewport bottom", () => {
  assert.deepEqual(calculateDropdownPosition({
    triggerTop: 700,
    triggerBottom: 736,
    viewportHeight: 800,
    menuHeight: 200,
  }), {
    opensAbove: true,
    maxHeight: 224,
  });
});

test("limits a dropdown to the available viewport height", () => {
  assert.deepEqual(calculateDropdownPosition({
    triggerTop: 160,
    triggerBottom: 196,
    viewportHeight: 300,
    menuHeight: 400,
  }), {
    opensAbove: true,
    maxHeight: 144,
  });
});
