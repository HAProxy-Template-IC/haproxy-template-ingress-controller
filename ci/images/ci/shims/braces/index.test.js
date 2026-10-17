"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const braces = require("./index.js");

test("passes brace-free globs through unchanged", () => {
  for (const glob of ["**/*.md", "#docs/*/site", "!node_modules/**", "docs/*/site/**"]) {
    assert.deepEqual(braces(glob, { expand: true, nodupes: true, keepEscaping: true }), [glob]);
  }
});

test("rejects any glob containing brace or escape characters", () => {
  for (const glob of ["docs/{a,b}/*.md", "{1..3}.md", "x\\{y", "{a", "a}"]) {
    assert.throws(() => braces(glob), /brace expansion/);
  }
});
