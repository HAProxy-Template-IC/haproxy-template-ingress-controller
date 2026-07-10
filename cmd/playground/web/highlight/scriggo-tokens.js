// External tokenizers for scriggo.grammar. Two modes, partitioned by grammar
// state (like the @lezer/liquid reference): `textTokens` runs at the template top
// level (plain text + tag starts), `interiorTokens` runs between a tag's opening
// and closing delimiters. All identifier classification is done here so the LR
// grammar stays a flat, conflict-free token stream.

import { ExternalTokenizer } from "@lezer/lr";
import {
  Text, Comment, ExprOpen, StmtOpen, BlockOpen,
  TagClose, Keyword, Bool, FunctionName, VariableName, PropertyName,
  String as StringTerm, Number as NumberTerm, Operator, Dot, Punct, Space,
} from "./scriggo.terms.js";

// Char codes.
const BRACE_L = 123, BRACE_R = 125, PERCENT = 37, HASH = 35, DASH = 45,
  PIPE = 124, DOT = 46, DQUOTE = 34, SQUOTE = 39, BACKTICK = 96, BACKSLASH = 92,
  NL = 10, CR = 13, SPACE = 32, TAB = 9,
  PAREN_L = 40, PAREN_R = 41, BRACKET_L = 91, BRACKET_R = 93, COMMA = 44, COLON = 58,
  EQ = 61, BANG = 33, LT = 60, GT = 62, AMP = 38, PLUS = 43, STAR = 42, SLASH = 47;

// Control words + the callable/logical operators spelled as words. `true false
// nil` are handled separately as Bool.
const KEYWORDS = new Set([
  "for", "in", "if", "else", "end", "range", "break", "continue", "return",
  "var", "const", "show", "switch", "case", "default", "fallthrough", "macro",
  "extends", "import", "using", "defer", "and", "or", "not", "contains",
]);
const BOOLS = new Set(["true", "false", "nil"]);

function isDigit(c) { return c >= 48 && c <= 57; }
function isWordStart(c) { return c === 95 || (c >= 65 && c <= 90) || (c >= 97 && c <= 122); }
function isWordChar(c) { return isWordStart(c) || isDigit(c); }
function isSpace(c) { return c === SPACE || c === TAB || c === NL || c === CR; }

export const textTokens = new ExternalTokenizer((input) => {
  const start = input.pos;
  for (;;) {
    const next = input.next;
    if (next < 0) break;
    if (next === BRACE_L) {
      const after = input.peek(1);
      if (after === BRACE_L) {                       // {{  or  {{-
        if (input.pos > start) break;
        input.acceptToken(ExprOpen, input.peek(2) === DASH ? 3 : 2);
        return;
      }
      if (after === HASH) {                           // {#  … whole comment … #}
        if (input.pos > start) break;
        let p = 2;
        for (;;) {
          const c = input.peek(p);
          if (c < 0) break;
          if (c === HASH && input.peek(p + 1) === BRACE_R) { p += 2; break; }
          p++;
        }
        input.acceptToken(Comment, p);
        return;
      }
      if (after === PERCENT) {                         // {%  {%-  {%%  {%%-
        if (input.pos > start) break;
        if (input.peek(2) === PERCENT) {
          input.acceptToken(BlockOpen, input.peek(3) === DASH ? 4 : 3);
        } else {
          input.acceptToken(StmtOpen, input.peek(2) === DASH ? 3 : 2);
        }
        return;
      }
    }
    input.advance();
    if (next === NL) break;   // break per line so incremental reparses stay local
  }
  if (input.pos > start) input.acceptToken(Text);
});

export const interiorTokens = new ExternalTokenizer((input) => {
  let next = input.next;
  if (next < 0) return;

  // Whitespace (including newlines — block statements span lines).
  if (isSpace(next)) {
    do { input.advance(); next = input.next; } while (next >= 0 && isSpace(next));
    input.acceptToken(Space);
    return;
  }

  // Closing delimiter, with optional `-` trim marker. Check `%%}` before `%}`.
  {
    const p = next === DASH ? 1 : 0;
    const c0 = input.peek(p), c1 = input.peek(p + 1), c2 = input.peek(p + 2);
    if (c0 === BRACE_R && c1 === BRACE_R) { input.acceptToken(TagClose, p + 2); return; }
    if (c0 === PERCENT && c1 === PERCENT && c2 === BRACE_R) { input.acceptToken(TagClose, p + 3); return; }
    if (c0 === PERCENT && c1 === BRACE_R) { input.acceptToken(TagClose, p + 2); return; }
  }

  // String literals: "…" '…' (stop at newline) and `…` raw (spans lines).
  if (next === DQUOTE || next === SQUOTE) {
    const q = next;
    input.advance();
    for (;;) {
      const c = input.next;
      if (c < 0 || c === NL) break;
      input.advance();
      if (c === BACKSLASH) { if (input.next >= 0) input.advance(); continue; }
      if (c === q) break;
    }
    input.acceptToken(StringTerm);
    return;
  }
  if (next === BACKTICK) {
    input.advance();
    for (;;) { const c = input.next; if (c < 0) break; input.advance(); if (c === BACKTICK) break; }
    input.acceptToken(StringTerm);
    return;
  }

  // Number.
  if (isDigit(next)) {
    input.advance();
    while (isDigit(input.next) || input.next === DOT) input.advance();
    input.acceptToken(NumberTerm);
    return;
  }

  // Identifier → Keyword | Bool | FunctionName | PropertyName | VariableName.
  if (isWordStart(next)) {
    let word = "";
    while (isWordChar(input.next)) { word += String.fromCharCode(input.next); input.advance(); }
    if (KEYWORDS.has(word)) { input.acceptToken(Keyword); return; }
    if (BOOLS.has(word)) { input.acceptToken(Bool); return; }
    // Immediately followed by `(` → a call.
    if (input.next === PAREN_L) { input.acceptToken(FunctionName); return; }
    // Look back past spaces for the preceding significant char.
    let off = -word.length - 1;
    let prev = input.peek(off);
    while (prev === SPACE || prev === TAB) { off--; prev = input.peek(off); }
    if (prev === DOT) { input.acceptToken(PropertyName); return; }       // member access
    if (prev === PIPE && input.peek(off - 1) !== PIPE) {                 // filter pipe (not `||`)
      input.acceptToken(FunctionName);
      return;
    }
    input.acceptToken(VariableName);
    return;
  }

  // Member dot.
  if (next === DOT) { input.advance(); input.acceptToken(Dot); return; }

  // Operators — two-char forms first.
  const c1 = input.peek(1);
  if ((next === COLON && c1 === EQ) || (next === EQ && c1 === EQ) ||
      (next === BANG && c1 === EQ) || (next === LT && c1 === EQ) ||
      (next === GT && c1 === EQ) || (next === AMP && c1 === AMP) ||
      (next === PIPE && c1 === PIPE)) {
    input.advance(); input.advance(); input.acceptToken(Operator); return;
  }
  if (next === EQ || next === LT || next === GT || next === PLUS || next === DASH ||
      next === STAR || next === SLASH || next === PERCENT || next === PIPE || next === BANG) {
    input.advance(); input.acceptToken(Operator); return;
  }

  // Punctuation.
  if (next === PAREN_L || next === PAREN_R || next === BRACKET_L || next === BRACKET_R ||
      next === COMMA || next === COLON) {
    input.advance(); input.acceptToken(Punct); return;
  }

  // Anything else: consume one char so the tokenizer never stalls.
  input.advance();
  input.acceptToken(Punct);
});
