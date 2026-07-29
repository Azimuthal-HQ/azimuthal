/**
 * Converting pasted markdown into document nodes.
 *
 * ## The problem, and the constraint on solving it
 *
 * Type-time markdown already works: `# ` makes a heading, `- ` a bullet, ``` a
 * fence. Pasted markdown did not — it arrived as literal text, which is the one
 * place a person is most likely to have markdown to hand.
 *
 * The constraint is that this must not become a THIRD markdown dialect. There is
 * already the server's (`internal/core/wiki/doc/markdown.go`, goldmark with GFM,
 * used to convert legacy pages) and TipTap's own input rules. A third one that
 * disagreed with either would mean the same text produced different documents
 * depending on how it arrived.
 *
 * So the dialect is not asserted, it is TESTED. `markdown_corpus.json` sits
 * beside the Go schema manifest and holds a sample of each construct with the
 * document it must produce. `markdown_corpus_test.go` checks the SERVER's
 * converter against it and `markdownPaste.test.ts` checks this one, so the two
 * implementations are held to the same bytes rather than to each other's
 * reputation. A construct outside the corpus is a construct this converter does
 * not claim.
 *
 * ## What happens to everything else
 *
 * It stays plain text. Not dropped, not guessed at, not turned into a
 * preservation placeholder — plain text.
 *
 * The placeholder point is worth stating because it looks like the obvious
 * answer and is wrong. Preservation ids are minted server-side when a document
 * is read, and publish resolves them against that same document; an id invented
 * in the browser during a paste would resolve to nothing, and publish would
 * refuse the whole write (`ErrUnknownPreservedContent`). Keeping unrepresentable
 * markdown as its own source text loses nothing and stores cleanly.
 *
 * ## When it declines entirely
 *
 * [markdownPasteContent] returns null for text that is not markdown-shaped, and
 * the editor then pastes it the way it always did. "See issue #42 in the
 * tracker" is prose containing a hash, not a heading, and a converter that
 * cannot tell the difference is worse than no converter.
 */
import type { CodexNode } from './schema';

/** The result of a paste conversion, or null when the text is ordinary prose. */
export function markdownPasteContent(text: string): CodexNode[] | null {
  if (!looksLikeMarkdown(text)) return null;
  const blocks = markdownToContent(text);
  return blocks.length > 0 ? blocks : null;
}

/**
 * The conversion itself, without the "is this markdown at all" question.
 *
 * Split out because the two halves are separately wrong-able and want separate
 * tests. The corpus checks THIS against the server's dialect for every
 * construct — including the prose ones, where the answer is a single paragraph
 * and agreeing about that still matters. [looksLikeMarkdown] is what decides
 * whether a paste is put through it, and it is checked on its own.
 */
export function markdownToContent(text: string): CodexNode[] {
  return parseBlocks(splitLines(text));
}

// ---------------------------------------------------------------------------
// Detection
// ---------------------------------------------------------------------------

const HEADING = /^(#{1,6})\s+(.*)$/;
const BULLET = /^(\s*)[-+*]\s+(.*)$/;
const ORDERED = /^(\s*)(\d+)\.\s+(.*)$/;
const TASK = /^\[([ xX])\]\s+(.*)$/;
const QUOTE = /^\s{0,3}>\s?(.*)$/;
const FENCE = /^\s{0,3}(?:```|~~~)\s*([A-Za-z0-9+#-]*)\s*$/;
const RULE = /^\s{0,3}(?:-{3,}|\*{3,}|_{3,})\s*$/;
const TABLE_DELIMITER = /^\s*\|?(?:\s*:?-{1,}:?\s*\|)+\s*:?-{0,}:?\s*\|?\s*$/;

/**
 * Whether the text is worth converting at all.
 *
 * Deliberately conservative. A false negative pastes as plain text, which is
 * what happens today and what the author typed; a false positive rewrites
 * somebody's prose into a structure they did not ask for. So a bare `#`, a lone
 * `*`, or a hash in the middle of a sentence are not enough — the marker has to
 * be in the position the construct actually occupies.
 */
export function looksLikeMarkdown(text: string): boolean {
  const lines = splitLines(text);
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i];
    if (HEADING.test(line) || QUOTE.test(line) || FENCE.test(line) || RULE.test(line)) return true;
    if (BULLET.test(line) || ORDERED.test(line)) return true;
    // A table is only a table with its delimiter row, which is what separates
    // `| a | b |` from an ASCII drawing somebody pasted.
    if (line.includes('|') && i + 1 < lines.length && TABLE_DELIMITER.test(lines[i + 1])) return true;
    if (INLINE_MARKER.test(line)) return true;
  }
  return false;
}

/**
 * Emphasis, code and links, in the forms the corpus covers.
 *
 * `#` is absent on purpose: it is the marker most likely to appear in ordinary
 * prose ("issue #42"), and a heading is already caught above by its position.
 *
 * The single-delimiter emphasis patterns require a non-space immediately inside
 * the delimiters, which is what separates `*emphasis*` from "a sentence with a
 * * in the middle". The underscore form additionally requires a non-word
 * character on the outside, so `some_var_name` is a identifier and not two
 * emphasised runs — snake_case is far commoner in this codebase's prose than
 * underscore emphasis is.
 */
const INLINE_MARKER =
  /(\*\*[^*\n]+\*\*)|(~~[^~\n]+~~)|(`[^`\n]+`)|((?<!!)\[[^\]\n]+\]\([^)\s]+\))|(\*[^\s*][^*\n]*\*)|((?<![A-Za-z0-9_])_[^\s_][^_\n]*_(?![A-Za-z0-9_]))/;

// ---------------------------------------------------------------------------
// Blocks
// ---------------------------------------------------------------------------

function splitLines(text: string): string[] {
  return text.replace(/\r\n?/g, '\n').split('\n');
}

function parseBlocks(lines: string[]): CodexNode[] {
  const out: CodexNode[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (line.trim() === '') {
      i += 1;
      continue;
    }

    const fence = FENCE.exec(line);
    if (fence) {
      const [node, next] = readFence(lines, i, fence[1]);
      out.push(node);
      i = next;
      continue;
    }

    if (RULE.test(line)) {
      out.push({ type: 'horizontalRule' });
      i += 1;
      continue;
    }

    const heading = HEADING.exec(line);
    if (heading) {
      out.push({
        type: 'heading',
        attrs: { level: heading[1].length },
        content: inlineNodes(heading[2].trim()),
      });
      i += 1;
      continue;
    }

    if (QUOTE.test(line)) {
      const [node, next] = readQuote(lines, i);
      out.push(node);
      i = next;
      continue;
    }

    if (i + 1 < lines.length && line.includes('|') && TABLE_DELIMITER.test(lines[i + 1])) {
      const [node, next] = readTable(lines, i);
      out.push(node);
      i = next;
      continue;
    }

    if (BULLET.test(line) || ORDERED.test(line)) {
      const [node, next] = readList(lines, i);
      out.push(node);
      i = next;
      continue;
    }

    const [node, next] = readParagraph(lines, i);
    if (node) out.push(node);
    i = next;
  }

  return out;
}

/**
 * A fenced code block.
 *
 * An unterminated fence still produces a code block, running to the end of the
 * paste. The alternative — treating it as a paragraph because the closing fence
 * is missing — would turn a truncated code sample into prose with backticks in
 * it, which is both less useful and harder to fix by hand.
 */
function readFence(lines: string[], start: number, language: string): [CodexNode, number] {
  const body: string[] = [];
  let i = start + 1;
  while (i < lines.length && !FENCE.test(lines[i])) {
    body.push(lines[i]);
    i += 1;
  }
  // The trailing newline is kept, because the server's converter keeps it: a
  // fenced block's source lines each end in one, and goldmark hands back the
  // lines rather than a trimmed body. Dropping it here would make the same
  // fence produce a different document depending on whether it was pasted or
  // converted from a legacy page, which is precisely what the corpus exists to
  // stop.
  const code = body.length === 0 ? '' : `${body.join('\n')}\n`;
  return [
    {
      type: 'codeBlock',
      attrs: { language },
      content: code === '' ? [] : [{ type: 'text', text: code }],
    },
    i < lines.length ? i + 1 : i,
  ];
}

/** A blockquote, whose stripped body is parsed as blocks in its own right. */
function readQuote(lines: string[], start: number): [CodexNode, number] {
  const inner: string[] = [];
  let i = start;
  while (i < lines.length) {
    const quoted = QUOTE.exec(lines[i]);
    if (!quoted) break;
    inner.push(quoted[1]);
    i += 1;
  }
  return [{ type: 'blockquote', content: parseBlocks(inner) }, i];
}

/**
 * A list, of whichever of the three kinds its items are.
 *
 * Any checkbox makes the whole list a task list, matching the server's
 * `listIsTaskList`: GFM allows a mixed list, and rendering the checked items as
 * plain bullets would lose their state.
 */
function readList(lines: string[], start: number): [CodexNode, number] {
  const items: { text: string; ordered: boolean }[] = [];
  let i = start;
  let firstStart = 1;

  while (i < lines.length) {
    const ordered = ORDERED.exec(lines[i]);
    const bullet = BULLET.exec(lines[i]);
    if (!ordered && !bullet) break;
    if (ordered && items.length === 0) firstStart = Number(ordered[2]);
    items.push({ text: (ordered ? ordered[3] : bullet![2]).trim(), ordered: !!ordered });
    i += 1;
  }

  const tasks = items.some((item) => TASK.test(item.text));
  if (tasks) {
    return [
      {
        type: 'taskList',
        content: items.map((item) => {
          const task = TASK.exec(item.text);
          const checked = !!task && task[1].toLowerCase() === 'x';
          const body = task ? task[2] : item.text;
          return {
            type: 'taskItem',
            attrs: { checked },
            content: [{ type: 'paragraph', content: inlineNodes(body) }],
          };
        }),
      },
      i,
    ];
  }

  const ordered = items[0].ordered;
  return [
    {
      type: ordered ? 'orderedList' : 'bulletList',
      ...(ordered ? { attrs: { start: firstStart } } : {}),
      content: items.map((item) => ({
        type: 'listItem',
        content: [{ type: 'paragraph', content: inlineNodes(item.text) }],
      })),
    },
    i,
  ];
}

/** A GFM pipe table. The first row supplies the header, as the server's does. */
function readTable(lines: string[], start: number): [CodexNode, number] {
  const rows: CodexNode[] = [];
  let i = start;
  let isHeader = true;

  while (i < lines.length && lines[i].includes('|')) {
    if (TABLE_DELIMITER.test(lines[i])) {
      i += 1;
      continue;
    }
    const cells = splitTableRow(lines[i]);
    rows.push({
      type: 'tableRow',
      content: cells.map((cell) => ({
        type: isHeader ? 'tableHeader' : 'tableCell',
        content: [{ type: 'paragraph', content: inlineNodes(cell) }],
      })),
    });
    isHeader = false;
    i += 1;
  }

  return [{ type: 'table', content: rows }, i];
}

function splitTableRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((cell) => cell.trim());
}

/**
 * A paragraph: consecutive non-blank lines that start no other construct.
 *
 * The lines are joined with a SPACE rather than a newline, because that is what
 * the server does with a markdown soft break — "a soft break is whitespace in
 * rendered markdown; keeping it as a space is what preserves word separation
 * across the wrapped line". A converter that kept the newline would produce a
 * different document for the same paste.
 */
function readParagraph(lines: string[], start: number): [CodexNode | null, number] {
  const parts: string[] = [];
  let i = start;
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === '') break;
    if (HEADING.test(line) || QUOTE.test(line) || FENCE.test(line) || RULE.test(line)) break;
    if (BULLET.test(line) || ORDERED.test(line)) break;
    parts.push(line.trim());
    i += 1;
  }
  if (parts.length === 0) return [null, start + 1];
  return [{ type: 'paragraph', content: inlineNodes(parts.join(' ')) }, i];
}

// ---------------------------------------------------------------------------
// Inline
// ---------------------------------------------------------------------------

/**
 * The inline constructs, in the order they are tried.
 *
 * Order is load-bearing between `**bold**` and `*italic*`: the italic pattern
 * would otherwise match the first two asterisks of a bold run and produce an
 * empty emphasis. Code is first because its content is literal — a backtick
 * span containing asterisks is code, not emphasis.
 */
const INLINE_RULES: { pattern: RegExp; build: (m: RegExpExecArray) => CodexNode }[] = [
  { pattern: /`([^`\n]+)`/, build: (m) => marked(m[1], 'code') },
  {
    // The negative lookbehind is what keeps `![alt](url)` — an image — from
    // being converted into a link over the text "alt" with a stray `!` left in
    // front of it. An image is deliberately NOT converted: an image node
    // addresses an attachment on THIS page, and publish verifies every image
    // reference against the page, so a node built from a pasted URL would make
    // the paste unpublishable. The markdown stays as text instead.
    pattern: /(?<!!)\[([^\]\n]+)\]\(([^)\s]+)\)/,
    build: (m) => ({
      type: 'text',
      text: m[1],
      marks: [{ type: 'link', attrs: { href: m[2] } }],
    }),
  },
  { pattern: /\*\*([^*\n]+)\*\*/, build: (m) => marked(m[1], 'bold') },
  { pattern: /__([^_\n]+)__/, build: (m) => marked(m[1], 'bold') },
  { pattern: /~~([^~\n]+)~~/, build: (m) => marked(m[1], 'strike') },
  { pattern: /\*([^*\n]+)\*/, build: (m) => marked(m[1], 'italic') },
  { pattern: /_([^_\n]+)_/, build: (m) => marked(m[1], 'italic') },
];

function marked(text: string, mark: string): CodexNode {
  return { type: 'text', text, marks: [{ type: mark }] };
}

/**
 * Splits a run of text into text nodes and marked text nodes.
 *
 * Single-level: `**bold with `code`**` produces bold text containing literal
 * backticks rather than a nested mark. That is a deliberate limit rather than an
 * oversight — nesting is where a hand-written converter and a real CommonMark
 * parser diverge fastest, and the corpus does not claim it. What it must never
 * do is DROP the characters, and it does not: they stay in the text.
 */
export function inlineNodes(text: string): CodexNode[] {
  if (text === '') return [];

  let earliest: { index: number; length: number; node: CodexNode } | null = null;
  for (const rule of INLINE_RULES) {
    const match = rule.pattern.exec(text);
    if (!match) continue;
    if (earliest === null || match.index < earliest.index) {
      earliest = { index: match.index, length: match[0].length, node: rule.build(match) };
    }
  }

  if (earliest === null) return [{ type: 'text', text }];

  const before = text.slice(0, earliest.index);
  const after = text.slice(earliest.index + earliest.length);
  return [
    ...(before === '' ? [] : [{ type: 'text', text: before } as CodexNode]),
    earliest.node,
    ...inlineNodes(after),
  ];
}
