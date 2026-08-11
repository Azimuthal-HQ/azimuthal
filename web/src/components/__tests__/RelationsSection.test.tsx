import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { RelationsSection } from '../RelationsSection';
import { RELATION_KINDS, type Relation } from '../../lib/api';

// A4. The shared relations surface: both ends must RENDER — a page target is
// a link to the page, not a bare string — and both from-sides must submit a
// typed target. The section is the one place the kind vocabulary is offered,
// so its select is pinned against the server's list.

const relationsHook = vi.fn();
const createMutate = vi.fn();
const createMutateAsync = vi.fn();
const deleteMutate = vi.fn();
const itemSearchHook = vi.fn();
const pageSuggestionsHook = vi.fn();

vi.mock('../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api')>();
  return {
    ...actual,
    useRelations: (...args: unknown[]) => relationsHook(...args),
    useCreateRelation: () => ({ mutate: createMutate, mutateAsync: createMutateAsync }),
    useDeleteRelation: () => ({ mutate: deleteMutate }),
    useItemSearch: (...args: unknown[]) => itemSearchHook(...args),
    usePageSuggestions: (...args: unknown[]) => pageSuggestionsHook(...args),
  };
});

const pageRelation: Relation = {
  id: 'rel-1',
  kind: 'wiki_link',
  direction: 'outgoing',
  far_readable: true,
  far_id: 'page-7',
  far_type: 'page',
  far_title: 'Deployment runbook',
  far_status: null, // pages have no status — the wire says null, not ''
  far_space_id: 'space-docs',
};

const restrictedRelation: Relation = {
  id: 'rel-2',
  kind: 'blocks',
  direction: 'incoming',
  far_readable: false,
  far_id: null,
  far_type: null,
  far_title: null,
  far_status: null,
  far_space_id: null,
};

// C4: an incoming link the read query unioned — a ticket that named this page.
// It must render its own identity and a link into Beacon, in the ticket's OWN
// space, so the page shows the reciprocal without any new write path.
const ticketIncomingRelation: Relation = {
  id: 'rel-3',
  kind: 'relates_to',
  direction: 'incoming',
  far_readable: true,
  far_id: 'ticket-42',
  far_type: 'ticket',
  far_title: 'Investigate the outage',
  far_status: 'open',
  far_space_id: 'space-desk',
};

function renderSection(entityType: 'project_item' | 'ticket' | 'page' = 'project_item') {
  return render(
    <MemoryRouter>
      <RelationsSection orgId="org-1" spaceId="space-1" entityType={entityType} entityId="ent-1" />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  relationsHook.mockReturnValue({ data: [] });
  itemSearchHook.mockReturnValue({ data: [] });
  pageSuggestionsHook.mockReturnValue({ data: [], isLoading: false });
});

describe('RelationsSection', () => {
  it('renders a relation to a page as its title, linked to the page in its OWN space', () => {
    relationsHook.mockReturnValue({ data: [pageRelation] });
    renderSection();

    const link = screen.getByTestId('relation-far-link-rel-1');
    expect(link).toHaveTextContent('Deployment runbook');
    // far_space_id, not this panel's space: relations link across spaces.
    expect(link).toHaveAttribute('href', '/codex/space-docs/pages/page-7');
  });

  it('renders an unreadable far side as the identity-free placeholder, with no link', () => {
    relationsHook.mockReturnValue({ data: [restrictedRelation] });
    renderSection();

    expect(screen.getByText('Restricted item')).toBeInTheDocument();
    expect(screen.queryByTestId('relation-far-link-rel-2')).not.toBeInTheDocument();
  });

  it('offers the full relation-kind vocabulary, wiki link included', () => {
    renderSection();

    const select = screen.getByLabelText('Relation kind') as HTMLSelectElement;
    const values = Array.from(select.options).map((o) => o.value);

    // Exhaustive in both directions against the ONE client-side vocabulary —
    // and that vocabulary is pinned to the server's ValidRelationKinds
    // verbatim, so a kind added on either side fails here until both agree.
    expect(values).toEqual(RELATION_KINDS.map((k) => k.value));
    expect(values).toEqual(['relates_to', 'blocks', 'is_blocked_by', 'duplicates', 'wiki_link']);
    expect(within(select).getByText('wiki link')).toBeInTheDocument();
  });

  it('selecting a suggested page submits a typed page target', () => {
    pageSuggestionsHook.mockImplementation((orgId: string, q: string) => ({
      data:
        orgId && q
          ? [{ page_id: 'page-7', title: 'Deployment runbook', space_id: 'space-docs', space_key: 'DOCS', space_name: 'Handbook' }]
          : [],
      isLoading: false,
    }));
    renderSection();

    // Switch the target picker to pages.
    fireEvent.change(screen.getByLabelText('Relation target type'), { target: { value: 'page' } });

    const input = screen.getByTestId('relation-page-ref');
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: 'runbook' } });
    fireEvent.click(screen.getByTestId('relation-page-ref-option-page-7'));

    expect(createMutate).toHaveBeenCalledWith({
      to_id: 'page-7',
      to_type: 'page',
      kind: 'relates_to',
    });
  });

  it('a ticket surface offers the page picker directly', () => {
    renderSection('ticket');

    // One target choice — no type select to render — and the picker is live.
    expect(screen.queryByLabelText('Relation target type')).not.toBeInTheDocument();
    expect(screen.getByTestId('relation-page-ref')).toBeInTheDocument();
  });

  it('the item search submits a typed work-item target', async () => {
    itemSearchHook.mockReturnValue({
      data: [{ id: 'item-9', title: 'Other item', status: 'open' }],
    });
    createMutateAsync.mockResolvedValue({});
    renderSection();

    fireEvent.change(screen.getByPlaceholderText('Search items…'), { target: { value: 'Other' } });
    fireEvent.click(await screen.findByText('Other item'));

    expect(createMutateAsync).toHaveBeenCalledWith({
      to_id: 'item-9',
      to_type: 'project_item',
      kind: 'relates_to',
    });
  });

  // C4: the panel a page's routes have been waiting for. A page links to a
  // page — the org-wide PageRefField, the maintainer's blocked cross-space
  // use case — and page→page defaults to a wiki link.
  it('renders on a page and offers the page picker directly, with no work-item target', () => {
    renderSection('page');

    expect(screen.getByTestId('relations-section')).toBeInTheDocument();
    // A page's only target is a page, so there is no target-type select — the
    // presence of one would mean the work-item picker leaked onto this surface.
    expect(screen.queryByLabelText('Relation target type')).not.toBeInTheDocument();
    expect(screen.getByTestId('relation-page-ref')).toBeInTheDocument();
  });

  it('a page defaults its relation kind to wiki link', () => {
    renderSection('page');

    const select = screen.getByLabelText('Relation kind') as HTMLSelectElement;
    // The default the C4 change adds — an item or ticket still defaults to
    // relates_to, so this assertion fails if the page near side is not special.
    expect(select.value).toBe('wiki_link');
  });

  it('creating a page→page relation submits a typed page target with the wiki_link default', () => {
    pageSuggestionsHook.mockImplementation((orgId: string, q: string) => ({
      data:
        orgId && q
          ? [{ page_id: 'page-9', title: 'Sibling page', space_id: 'space-b', space_key: 'DOCB', space_name: 'Book B' }]
          : [],
      isLoading: false,
    }));
    renderSection('page');

    const input = screen.getByTestId('relation-page-ref');
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: 'sibling' } });
    fireEvent.click(screen.getByTestId('relation-page-ref-option-page-9'));

    expect(createMutate).toHaveBeenCalledWith({
      to_id: 'page-9',
      to_type: 'page',
      kind: 'wiki_link',
    });
  });

  it('a page can still submit any kind — the wiki_link default does not lock the select', () => {
    pageSuggestionsHook.mockImplementation((orgId: string, q: string) => ({
      data:
        orgId && q
          ? [{ page_id: 'page-9', title: 'Sibling page', space_id: 'space-b', space_key: 'DOCB', space_name: 'Book B' }]
          : [],
      isLoading: false,
    }));
    renderSection('page');

    // Override the default, then submit: the chosen kind must reach the wire.
    fireEvent.change(screen.getByLabelText('Relation kind'), { target: { value: 'relates_to' } });
    const input = screen.getByTestId('relation-page-ref');
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: 'sibling' } });
    fireEvent.click(screen.getByTestId('relation-page-ref-option-page-9'));

    expect(createMutate).toHaveBeenCalledWith({
      to_id: 'page-9',
      to_type: 'page',
      kind: 'relates_to',
    });
  });

  it('renders an incoming relation from a ticket with its identity and a link into its own space', () => {
    relationsHook.mockReturnValue({ data: [ticketIncomingRelation] });
    renderSection('page');

    const link = screen.getByTestId('relation-far-link-rel-3');
    expect(link).toHaveTextContent('Investigate the outage');
    // The ticket's OWN space, in Beacon — the reciprocal renders with a real
    // identity, not the restricted placeholder.
    expect(link).toHaveAttribute('href', '/beacon/space-desk/tickets/ticket-42');
  });

  it('keeps an unreadable far side identity-free on the page surface too', () => {
    relationsHook.mockReturnValue({ data: [restrictedRelation] });
    renderSection('page');

    expect(screen.getByText('Restricted item')).toBeInTheDocument();
    expect(screen.queryByTestId('relation-far-link-rel-2')).not.toBeInTheDocument();
  });
});
