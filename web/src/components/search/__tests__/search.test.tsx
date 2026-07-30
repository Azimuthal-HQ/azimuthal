import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { splitSnippet, type SearchHit } from '../../../lib/api';
import { ResultRow } from '../../../pages/home/SearchPage';
import { hitHref, hitReference } from '../searchLinks';

const STX = String.fromCharCode(2);
const ETX = String.fromCharCode(3);

function hit(over: Partial<SearchHit> = {}): SearchHit {
  return {
    module: 'codex',
    id: 'aaaaaaaa-0000-0000-0000-000000000001',
    title: 'Kestrel runbook',
    origin: 'space',
    space_id: 'bbbbbbbb-0000-0000-0000-000000000002',
    space_key: 'SD',
    space_name: 'Service Desk',
    updated_at: '2026-07-30T00:00:00Z',
    ...over,
  };
}

describe('splitSnippet', () => {
  it('splits the server delimiters into plain and matched runs', () => {
    expect(splitSnippet(`before ${STX}kestrel${ETX} after`)).toEqual([
      { text: 'before ', match: false },
      { text: 'kestrel', match: true },
      { text: ' after', match: false },
    ]);
  });

  it('treats stored markup as ordinary text, never as structure', () => {
    // The whole reason the delimiters are control characters. If a caller ever
    // rendered this with innerHTML the tag would become an element; splitting
    // it into text runs is what makes that impossible by construction.
    const parts = splitSnippet(`a <b>bold</b> ${STX}kestrel${ETX}`);
    expect(parts[0]).toEqual({ text: 'a <b>bold</b> ', match: false });
    expect(parts.some((p) => p.match && p.text === 'kestrel')).toBe(true);
  });

  it('keeps an unterminated run rather than dropping it', () => {
    // A truncated fragment is a display nuisance; losing the text is data loss.
    expect(splitSnippet(`head ${STX}tail`)).toEqual([
      { text: 'head ', match: false },
      { text: 'tail', match: false },
    ]);
  });

  it('handles a snippet with no match and an empty snippet', () => {
    expect(splitSnippet('plain text')).toEqual([{ text: 'plain text', match: false }]);
    expect(splitSnippet('')).toEqual([]);
  });
});

describe('hitHref', () => {
  it('routes a readable hit into its space, per module', () => {
    expect(hitHref(hit({ module: 'codex' }))).toBe(
      '/codex/bbbbbbbb-0000-0000-0000-000000000002/pages/aaaaaaaa-0000-0000-0000-000000000001',
    );
    expect(hitHref(hit({ module: 'beacon' }))).toContain('/beacon/');
    expect(hitHref(hit({ module: 'beacon' }))).toContain('/tickets/');
    expect(hitHref(hit({ module: 'vector' }))).toContain('/backlog/');
  });

  it('routes a SHARE-ONLY hit through /shared, never into a space', () => {
    // A share-only hit carries no space id by design, so a space-scoped URL
    // could only be built by inventing one. /shared is the route whose access
    // is governed by the share itself.
    const shared = hit({ origin: 'share', space_id: undefined, space_key: undefined, space_name: undefined });
    expect(hitHref(shared)).toBe('/shared/page/aaaaaaaa-0000-0000-0000-000000000001');
    expect(hitHref({ ...shared, module: 'beacon' })).toBe(
      '/shared/ticket/aaaaaaaa-0000-0000-0000-000000000001',
    );
    expect(hitHref({ ...shared, module: 'vector' })).toBe(
      '/shared/project_item/aaaaaaaa-0000-0000-0000-000000000001',
    );
    expect(hitHref(shared)).not.toContain('undefined');
  });
});

describe('hitReference', () => {
  it('uses the item key for Vector and composes the ref for Beacon', () => {
    expect(hitReference(hit({ module: 'vector', item_key: 'VEC-14' }))).toBe('VEC-14');
    expect(hitReference(hit({ module: 'beacon', number: 42 }))).toBe('SD-42');
  });

  it('has no reference when the parts are missing — and never a partial one', () => {
    // A share-only Beacon hit has no space_key, so there is no ref to compose.
    // "undefined-42" would be worse than nothing.
    expect(hitReference(hit({ module: 'beacon', number: 42, space_key: undefined }))).toBeNull();
    expect(hitReference(hit({ module: 'codex' }))).toBeNull();
  });
});

describe('ResultRow disclosure', () => {
  const renderRow = (h: SearchHit) =>
    render(
      <MemoryRouter>
        <ResultRow hit={h} />
      </MemoryRouter>,
    );

  it('names the container for a hit in a space the viewer can read', () => {
    renderRow(hit());
    expect(screen.getByText('Service Desk')).toBeInTheDocument();
    expect(screen.queryByText('Shared')).not.toBeInTheDocument();
  });

  it('shows provenance and NO container for a share-only hit', () => {
    // The wire response omits the container fields entirely (matrix case 16);
    // this asserts the surface does not reintroduce one, and does not render a
    // placeholder like "Unknown space" in its place.
    renderRow(hit({ origin: 'share', space_id: undefined, space_key: undefined, space_name: undefined }));
    expect(screen.getByText('Shared')).toBeInTheDocument();
    expect(screen.queryByText('Service Desk')).not.toBeInTheDocument();
    expect(screen.queryByText(/unknown/i)).not.toBeInTheDocument();
  });

  it('renders a snippet as text, with the match marked', () => {
    renderRow(hit({ snippet: `see ${STX}kestrel${ETX} here` }));
    const mark = screen.getByText('kestrel');
    expect(mark.tagName).toBe('MARK');
    // The delimiters themselves never reach the document.
    expect(document.body.textContent).not.toContain(STX);
    expect(document.body.textContent).not.toContain(ETX);
  });
});
