"use strict";

// Real braces returns a pattern without {, } or \ unchanged; anything else is refused, not guessed.
const needsExpansion = /[{}\\]/;

module.exports = (pattern) => {
  const input = String(pattern);
  if (needsExpansion.test(input)) {
    throw new Error(
      `markdownlint glob '${input}' uses brace expansion, which the CI image doesn't support ` +
        "(braces is stubbed out for GHSA-vfj7-8cjw-p6xm), so the file set can't be resolved. " +
        "List the alternatives as separate globs.",
    );
  }
  return [input];
};
