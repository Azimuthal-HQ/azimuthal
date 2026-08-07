import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { CodexTag, TagEntityType } from '../../../lib/api';
import { EntityTags } from '../EntityTags';

/**
 * The entity tag surface (U4, generalized by the entity-tags convergence), in
 * both of its modes. This is the battery that covered PageTags before the
 * component went entity-generic; every page assertion still holds, exercised
 * through entityType="page", and the ticket siblings at the end pin that the
 * kind changes which hooks are called — never the surface's behaviour.
 *
 * The assertions that matter here are about the SHAPE of what is sent, not
 * about whether anything was sent. `useSetEntityTags` is a whole-set
 * replacement: adding a tag means PUTting every tag the entity should end up
 * with, and removing one means PUTting the shorter list. A component that sent
 * a delta — `['new']` on an add, `['gone']` on a remove — would look correct in
 * the DOM and would silently strip every other tag off the entity on the first
 * edit. So each mutation test asserts the exact array, and would fail on a
 * delta.
 *
 * The last page-mode case pins the inline-#tag copy. It is not decoration: the
 * decided semantic is that publishing merges body #tags into this list and that
 * removing a body #tag does NOT remove the tag, and a person who is not told
 * that will reasonably assume the opposite.
 */

const { setTagsMock, entityTagsArgs, setEntityTagsArgs } = vi.hoisted(() => ({
  setTagsMock: vi.fn(),
  entityTagsArgs: { calls: [] as unknown[][] },
  setEntityTagsArgs: { calls: [] as unknown[][] },
}));

function tag(name: string, slug = name.toLowerCase()): CodexTag {
  return {
    id: `tag-${slug}`,
    org_id: 'org-1',
    slug,
    name,
    created_at: '2026-01-01T00:00:00Z',
  };
}

let entityTags: CodexTag[] = [];
let orgTags: CodexTag[] = [];
let setTagsError: unknown = null;

vi.mock('../../../lib/auth', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../lib/auth')>()),
  getCurrentOrgId: () => 'org-1',
}));

// The importOriginal spread is load-bearing: friendlyErrorMessage dispatches on
// `instanceof APIError`, so the real class has to come through this mock or the
// refusal path would silently fall back to its generic message.
vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useEntityTags: (...args: unknown[]) => {
      entityTagsArgs.calls.push(args);
      return { data: entityTags, isLoading: false, error: null };
    },
    useOrgTags: () => ({ data: orgTags, isLoading: false, error: null }),
    useSetEntityTags: (...args: unknown[]) => {
      setEntityTagsArgs.calls.push(args);
      return { mutate: setTagsMock, isPending: false, error: setTagsError };
    },
  };
});

function renderTags(editable: boolean, entityType: TagEntityType = 'page') {
  return render(
    <MemoryRouter>
      <EntityTags
        entityType={entityType}
        spaceId="space-1"
        entityId="entity-1"
        editable={editable}
      />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  entityTags = [];
  orgTags = [];
  setTagsError = null;
  entityTagsArgs.calls = [];
  setEntityTagsArgs.calls = [];
});

describe('reading mode', () => {
  it('renders a chip per tag, each linking to the tag browse', () => {
    entityTags = [tag('runbook'), tag('On Call', 'on_call')];
    renderTags(false);

    const chips = screen.getAllByTestId('codex-page-tag');
    expect(chips).toHaveLength(2);
    expect(chips[0]).toHaveTextContent('runbook');
    expect(chips[0]).toHaveAttribute('href', '/codex/space-1/tags/runbook');
    // The LABEL travels in the path, percent-encoded — not the slug. A chip
    // that linked by slug would send `on_call` where the display name is
    // `On Call`, and this asserts the direction tagLinks.ts chose.
    expect(chips[1]).toHaveAttribute('href', '/codex/space-1/tags/On%20Call');
    expect(chips[1]).toHaveAttribute('data-slug', 'on_call');
  });

  it('renders nothing at all when the entity has no tags', () => {
    entityTags = [];
    const { container } = renderTags(false);

    expect(screen.queryByTestId('codex-page-tags')).not.toBeInTheDocument();
    // Not merely "no chips": an empty label row on every untagged entity is
    // the thing being prevented, so the component must emit no markup at all.
    expect(container).toBeEmptyDOMElement();
  });

  it('offers no editing affordances', () => {
    entityTags = [tag('runbook')];
    renderTags(false);

    expect(screen.queryByTestId('codex-tag-input')).not.toBeInTheDocument();
    expect(screen.queryByTestId('codex-tag-remove')).not.toBeInTheDocument();
  });
});

describe('editing mode', () => {
  it('sends the whole new list when a tag is added, not just the new one', () => {
    entityTags = [tag('runbook'), tag('ops')];
    renderTags(true);

    fireEvent.change(screen.getByTestId('codex-tag-input'), { target: { value: 'incident' } });
    fireEvent.keyDown(screen.getByTestId('codex-tag-input'), { key: 'Enter' });

    expect(setTagsMock).toHaveBeenCalledWith(['runbook', 'ops', 'incident']);
  });

  it('commits on a comma as well as on Enter', () => {
    entityTags = [];
    renderTags(true);

    fireEvent.change(screen.getByTestId('codex-tag-input'), { target: { value: 'incident' } });
    fireEvent.keyDown(screen.getByTestId('codex-tag-input'), { key: ',' });

    expect(setTagsMock).toHaveBeenCalledWith(['incident']);
  });

  it('sends the shorter list when a chip is removed', () => {
    entityTags = [tag('runbook'), tag('ops'), tag('incident')];
    renderTags(true);

    fireEvent.click(screen.getAllByTestId('codex-tag-remove')[1]);

    expect(setTagsMock).toHaveBeenCalledWith(['runbook', 'incident']);
  });

  it('removes the last chip on Backspace in an empty input', () => {
    entityTags = [tag('runbook'), tag('ops')];
    renderTags(true);

    fireEvent.keyDown(screen.getByTestId('codex-tag-input'), { key: 'Backspace' });

    expect(setTagsMock).toHaveBeenCalledWith(['runbook']);
  });

  it('leaves the tags alone when Backspace is pressed with text in the input', () => {
    entityTags = [tag('runbook')];
    renderTags(true);

    fireEvent.change(screen.getByTestId('codex-tag-input'), { target: { value: 'op' } });
    fireEvent.keyDown(screen.getByTestId('codex-tag-input'), { key: 'Backspace' });

    // Backspace is first and foremost a text-editing key. Deleting a tag while
    // the author was correcting a typo would be a destructive surprise.
    expect(setTagsMock).not.toHaveBeenCalled();
  });

  it('suggests org tags matching what is typed, and excludes ones already carried', () => {
    entityTags = [tag('runbook')];
    orgTags = [tag('runbook'), tag('runtime'), tag('ops')];
    renderTags(true);

    fireEvent.change(screen.getByTestId('codex-tag-input'), { target: { value: 'run' } });

    const suggestions = screen.getAllByTestId('codex-tag-suggestion');
    expect(suggestions.map((s) => s.textContent)).toEqual(['runtime']);
  });

  it('commits a name that matches no existing tag, because typing one is how tags are created', () => {
    entityTags = [];
    orgTags = [tag('ops')];
    renderTags(true);

    fireEvent.change(screen.getByTestId('codex-tag-input'), { target: { value: 'brand-new' } });
    expect(screen.queryByTestId('codex-tag-suggestion')).not.toBeInTheDocument();

    fireEvent.keyDown(screen.getByTestId('codex-tag-input'), { key: 'Enter' });
    expect(setTagsMock).toHaveBeenCalledWith(['brand-new']);
  });

  it('does not write when the typed name is already carried', () => {
    entityTags = [tag('ops')];
    renderTags(true);

    fireEvent.change(screen.getByTestId('codex-tag-input'), { target: { value: 'OPS' } });
    fireEvent.keyDown(screen.getByTestId('codex-tag-input'), { key: 'Enter' });

    expect(setTagsMock).not.toHaveBeenCalled();
  });

  it('shows the server’s own words when a name cannot become a tag', async () => {
    const { APIError } = await import('../../../lib/api');
    setTagsError = new APIError(400, {
      error: {
        code: 'VALIDATION_ERROR',
        message: 'A tag name must contain at least one letter or number.',
        request_id: 'req-1',
      },
    });
    entityTags = [];
    renderTags(true);

    // Not a generic fallback: friendlyErrorMessage passes VALIDATION_ERROR
    // through, and this fails if the component swallows it or if the mock has
    // lost the real APIError class.
    expect(screen.getByTestId('codex-page-tags-error')).toHaveTextContent(
      'A tag name must contain at least one letter or number.',
    );
  });

  it('states that body #tags are merged in on publish and that this list is the authority', () => {
    entityTags = [];
    renderTags(true);

    const note = screen.getByTestId('codex-page-tags').textContent ?? '';
    // Both halves of the decided semantic, asserted separately: the merge on
    // publish, and — the half a reader would otherwise guess wrong — that
    // deleting the #tag from the prose does not remove the tag.
    expect(note).toMatch(/#tag[\s\S]*added to this list when you publish/i);
    expect(note).toMatch(/deleting the #tag from the body will not take the tag off the page/i);
  });
});

describe('on a ticket', () => {
  it('renders chips and edits with the same whole-set semantics, through the ticket hooks', () => {
    entityTags = [tag('runbook')];
    renderTags(true, 'ticket');

    // The kind reaches both hooks: it is what selects the API route, and a
    // component that hardcoded 'page' would write a ticket's tags to a page.
    expect(entityTagsArgs.calls[0]).toEqual(['ticket', 'space-1', 'entity-1']);
    expect(setEntityTagsArgs.calls[0]).toEqual(['ticket', 'space-1', 'entity-1']);

    fireEvent.change(screen.getByTestId('codex-tag-input'), { target: { value: 'incident' } });
    fireEvent.keyDown(screen.getByTestId('codex-tag-input'), { key: 'Enter' });
    expect(setTagsMock).toHaveBeenCalledWith(['runbook', 'incident']);
  });

  it('links a reading chip into the beacon module when no module is in the URL', () => {
    entityTags = [tag('runbook')];
    renderTags(false, 'ticket');

    expect(screen.getAllByTestId('codex-page-tag')[0]).toHaveAttribute(
      'href',
      '/beacon/space-1/tags/runbook',
    );
  });

  it('does not show the page-only inline-#tag note', () => {
    entityTags = [];
    renderTags(true, 'ticket');

    // Tickets have no document body, so the sentence about publishing #tags
    // would be a false statement on this surface.
    expect(screen.getByTestId('codex-page-tags').textContent ?? '').not.toMatch(/publish/i);
  });
});
