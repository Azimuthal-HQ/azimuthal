import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import { looksLikeMarkdown, markdownPasteContent, markdownToContent } from './markdownPaste';
import type { CodexNode } from './schema';

/**
 * The paste converter, held to the SERVER's dialect rather than to its own.
 *
 * `internal/core/wiki/doc/markdown_corpus.json` holds one sample of each
 * construct with the document it must produce, and those documents were
 * generated from the server's goldmark converter — so they record what the
 * legacy-page conversion actually does, not what anybody believes it does. The
 * Go half of this check is `markdown_corpus_test.go`.
 *
 * That is what keeps this from becoming a third markdown dialect. There were
 * already two — the server's converter and TipTap's type-time input rules — and
 * three implementations that agree by inspection agree only until somebody edits
 * one of them.
 *
 * Both halves of the corpus matter. `prose-with-a-hash` is in it precisely
 * because the converter must DECLINE it: "See issue #42 in the tracker" is
 * prose, and a converter that turned it into a heading would be worse than no
 * converter at all.
 */

const corpusPath = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../internal/core/wiki/doc/markdown_corpus.json',
);

interface CorpusCase {
  name: string;
  why: string;
  markdown: string;
  doc: { type: 'doc'; content: CodexNode[] };
}

function readCorpus(): CorpusCase[] {
  return (JSON.parse(readFileSync(corpusPath, 'utf8')) as { cases: CorpusCase[] }).cases;
}

/**
 * The corpus cases that carry no markdown marker at all.
 *
 * They still have to CONVERT identically — a paragraph is a construct like any
 * other — but a paste of one must not be put through the converter in the first
 * place, because there is nothing to convert and the editor's own paste
 * handling is what an author expects. The two properties are checked
 * separately, below.
 */
const NOT_MARKDOWN_SHAPED = new Set(['paragraph', 'paragraph-soft-break', 'prose-with-a-hash']);

describe('the paste converter matches the server’s markdown dialect', () => {
  it('finds the corpus where the Go package keeps it', () => {
    // A move or a rename on either side must fail loudly here rather than
    // silently skipping every comparison below.
    expect(() => readCorpus()).not.toThrow();
    expect(readCorpus().length).toBeGreaterThan(10);
  });

  for (const kase of readCorpus()) {
    it(`converts ${kase.name} to the same document the server does`, () => {
      // Whole-document equality, not a spot check on one attribute: a converter
      // that produced the right node type with the wrong attrs, or the right
      // attrs around the wrong text, would satisfy anything less.
      expect({ type: 'doc', content: markdownToContent(kase.markdown) }).toEqual(kase.doc);
    });
  }

  for (const kase of readCorpus()) {
    if (NOT_MARKDOWN_SHAPED.has(kase.name)) continue;

    it(`recognises ${kase.name} as worth converting`, () => {
      expect(markdownPasteContent(kase.markdown)).not.toBeNull();
    });
  }
});

describe('the paste converter declines ordinary prose', () => {
  for (const kase of readCorpus()) {
    if (!NOT_MARKDOWN_SHAPED.has(kase.name)) continue;

    it(`leaves ${kase.name} alone`, () => {
      expect(markdownPasteContent(kase.markdown)).toBeNull();
      expect(looksLikeMarkdown(kase.markdown)).toBe(false);
    });
  }

  it('does not mangle an issue number into a heading', () => {
    // Named separately from the corpus loop because it is the specific case the
    // feature was required to get right, and a reader looking for it should not
    // have to know it lives in a JSON file.
    expect(markdownPasteContent('See issue #42 in the tracker.')).toBeNull();
    expect(markdownPasteContent('#42')).toBeNull();
    expect(markdownPasteContent('C# and F# are languages')).toBeNull();
  });

  it('does not treat a lone asterisk or dash inside a sentence as a list', () => {
    expect(markdownPasteContent('A sentence with a * in the middle.')).toBeNull();
    expect(markdownPasteContent('Nine - ten - eleven.')).toBeNull();
  });

  it('recognises a heading only when the hash is in the heading position', () => {
    expect(looksLikeMarkdown('# Runbook')).toBe(true);
    expect(looksLikeMarkdown('Runbook #1')).toBe(false);
    // No space after the hashes: markdown does not make this a heading either,
    // and the type-time rules agree — `#design ` is a TAG, not an H1.
    expect(looksLikeMarkdown('#Runbook')).toBe(false);
  });
});

describe('constructs outside the corpus stay as text', () => {
  it('keeps a raw HTML block as its own source text', () => {
    // Never a preservation placeholder. Preservation ids are minted server-side
    // when a document is read and resolved against that same document; an id
    // invented during a paste would resolve to nothing and publish would refuse
    // the entire write. Keeping the source text loses nothing.
    const content = markdownPasteContent('# Title\n\n<div class="callout">x</div>');
    expect(content).not.toBeNull();
    const flat = JSON.stringify(content);
    expect(flat).toContain('<div class=\\"callout\\">x</div>');
    expect(flat).not.toContain('unknownContent');
    expect(flat).not.toContain('az_id');
  });

  it('keeps an image reference as text rather than guessing at a node', () => {
    // An image node addresses an attachment on THIS page. A pasted URL is not
    // one, and publish verifies every image reference against the page — so a
    // converted image node would make the paste unpublishable.
    const content = markdownPasteContent('# Title\n\n![alt](https://example.com/a.png)');
    expect(JSON.stringify(content)).toContain('![alt](https://example.com/a.png)');
    expect(JSON.stringify(content)).not.toContain('"type":"image"');
  });

  it('keeps nested emphasis inside code as literal text rather than nesting marks', () => {
    // A documented limit, not an oversight: nesting is where a hand-written
    // converter and a real CommonMark parser diverge fastest. What it must
    // never do is drop the characters.
    const content = markdownPasteContent('Use `a **b** c` here.');
    expect(JSON.stringify(content)).toContain('a **b** c');
  });
});
