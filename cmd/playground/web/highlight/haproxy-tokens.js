// External tokenizer + line-state context tracker for haproxy.grammar.
// One tokenizer emits every token; the context tracker records whether the next
// word is the first on its line (Section/Directive candidate) and whether it
// immediately follows a Section keyword (ProxyName candidate).

import { ExternalTokenizer, ContextTracker } from "@lezer/lr";
import {
  Newline, Space, TemplateTag, Comment, Section, Directive, ProxyName,
  Number as NumberTerm, StringTok, Word,
} from "./haproxy.terms.js";

// Char codes.
const BRACE_L = 123, BRACE_R = 125, PERCENT = 37, HASH = 35,
  NL = 10, CR = 13, SPACE = 32, TAB = 9, DQUOTE = 34, SQUOTE = 39;

// Section keywords: their first-word occurrence opens a config section.
const SECTIONS = new Set([
  "global", "defaults", "frontend", "backend", "listen", "peers", "resolvers",
  "cache", "ring", "userlist", "mailers", "program", "http-errors", "fcgi-app",
  "log-forward", "crt-store", "traces", "acme", "ruleset",
]);

// A numeric-ish token: a status code, port, IP, or number. Broader than a bare
// leading digit so ports written `:80` and globs like `*:443` also count.
const NUM_RE = /^[-*]?:?\d[\w.:/]*$/;

// Context bits: FIRST = next word is the first on this line; AFTERSEC = the
// previous word was a Section keyword.
const FIRST = 1, AFTERSEC = 2;

export const trackLine = new ContextTracker({
  start: FIRST,
  shift(context, term) {
    if (term === Newline) return FIRST;
    // Neither whitespace nor a template tag consumes the "first word" slot: a
    // line that opens with `{{ env }}frontend http` still has `frontend` as its
    // first real word, and a tag between a section keyword and its proxy name
    // (`frontend {{ name }}`) keeps the AFTERSEC candidacy.
    if (term === Space || term === TemplateTag) return context;
    if (term === Section) return AFTERSEC;
    if (term === Directive || term === ProxyName || term === NumberTerm ||
        term === StringTok || term === Word || term === Comment) return 0;
    return context;
  },
  hash(context) { return context; },
});

function isSpace(c) { return c === SPACE || c === TAB; }
function isBreak(c) { return c === NL || c === CR; }

// A template tag start: `{` followed by `{`, `%`, or `#`.
function tagKind(input) {
  if (input.next !== BRACE_L) return 0;
  const a = input.peek(1);
  if (a === BRACE_L) return 1;    // {{ … }}
  if (a === HASH) return 2;       // {# … #}
  if (a === PERCENT) return input.peek(2) === PERCENT ? 3 : 4;  // {%% … %%} / {% … %}
  return 0;
}

// Consume a whole template tag (including its close delimiter, or to EOF).
function consumeTag(input, kind) {
  input.advance(); input.advance();          // past `{{` / `{#` / `{%`
  if (kind === 3) input.advance();           // past the second `%` of `{%%`
  // close: 1 -> }}, 2 -> #}, 3 -> %%}, 4 -> %}
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

export const haproxyTokens = new ExternalTokenizer((input, stack) => {
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

  if (next === HASH) {                 // `#` … end-of-line comment
    while (input.next >= 0 && !isBreak(input.next)) input.advance();
    input.acceptToken(Comment);
    return;
  }

  // A word: a maximal run stopping at whitespace, a `#`, or a tag start.
  let word = "";
  for (;;) {
    const c = input.next;
    if (c < 0 || isSpace(c) || isBreak(c) || c === HASH) break;
    if (tagKind(input)) break;
    word += String.fromCharCode(c);
    input.advance();
  }
  if (!word) { input.advance(); input.acceptToken(Word); return; }  // defensive

  const ctx = stack.context;
  if (ctx & FIRST) {
    input.acceptToken(SECTIONS.has(word.toLowerCase()) ? Section : Directive);
  } else if (ctx & AFTERSEC) {
    input.acceptToken(ProxyName);
  } else if (NUM_RE.test(word)) {
    input.acceptToken(NumberTerm);
  } else if (word[0] === '"' || word[0] === "'") {
    input.acceptToken(StringTok);
  } else {
    input.acceptToken(Word);
  }
});
