// External tokenizer + column-state context tracker for map.grammar. The first
// word of a line is a MapKey, the rest MapValue. `#` comments and Scriggo tags
// are recognised anywhere.

import { ExternalTokenizer, ContextTracker } from "@lezer/lr";
import { Newline, Space, TemplateTag, Comment, MapKey, MapValue } from "./map.terms.js";

// Char codes.
const BRACE_L = 123, BRACE_R = 125, PERCENT = 37, HASH = 35,
  NL = 10, CR = 13, SPACE = 32, TAB = 9;

// FIRST: the next word is the first column of its line (a MapKey).
const FIRST = 1;

export const trackColumn = new ContextTracker({
  start: FIRST,
  shift(context, term) {
    if (term === Newline) return FIRST;
    if (term === Space) return context;
    if (term === MapKey || term === MapValue || term === TemplateTag || term === Comment) return 0;
    return context;
  },
  hash(context) { return context; },
});

function isSpace(c) { return c === SPACE || c === TAB; }
function isBreak(c) { return c === NL || c === CR; }

function tagKind(input) {
  if (input.next !== BRACE_L) return 0;
  const a = input.peek(1);
  if (a === BRACE_L) return 1;
  if (a === HASH) return 2;
  if (a === PERCENT) return input.peek(2) === PERCENT ? 3 : 4;
  return 0;
}

function consumeTag(input, kind) {
  input.advance(); input.advance();
  if (kind === 3) input.advance();
  for (;;) {
    const c = input.next;
    if (c < 0) break;
    if (kind === 1 && c === BRACE_R && input.peek(1) === BRACE_R) { input.advance(); input.advance(); break; }
    if (kind === 2 && c === HASH && input.peek(1) === BRACE_R) { input.advance(); input.advance(); break; }
    if (kind === 3 && c === PERCENT && input.peek(1) === PERCENT && input.peek(2) === BRACE_R) { input.advance(); input.advance(); input.advance(); break; }
    if (kind === 4 && c === PERCENT && input.peek(1) === BRACE_R) { input.advance(); input.advance(); break; }
    input.advance();
  }
}

export const mapTokens = new ExternalTokenizer((input, stack) => {
  const next = input.next;
  if (next < 0) return;

  if (isBreak(next)) { input.advance(); input.acceptToken(Newline); return; }
  if (isSpace(next)) {
    do { input.advance(); } while (isSpace(input.next));
    input.acceptToken(Space);
    return;
  }

  const kind = tagKind(input);
  if (kind) { consumeTag(input, kind); input.acceptToken(TemplateTag); return; }

  if (next === HASH) {
    while (input.next >= 0 && !isBreak(input.next)) input.advance();
    input.acceptToken(Comment);
    return;
  }

  // A word: stop at whitespace, `#`, or a tag start.
  let empty = true;
  for (;;) {
    const c = input.next;
    if (c < 0 || isSpace(c) || isBreak(c) || c === HASH) break;
    if (tagKind(input)) break;
    empty = false;
    input.advance();
  }
  if (empty) { input.advance(); input.acceptToken(MapValue); return; }  // defensive
  input.acceptToken((stack.context & FIRST) ? MapKey : MapValue);
});
