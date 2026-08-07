import type { ReactNode } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { buildLanes, resolveDropTarget, laneKeyOf, SprintBoardPage } from '../SprintBoardPage';
import { queryKeys, type ProjectItem } from '../../../lib/api';
import type { DragEndEvent, DragStartEvent } from '@dnd-kit/core';

// W4: swimlanes and drop resolution. These are the pure pieces of the board's
// grouping and drag logic; the rendered interplay of lanes, columns, WIP and
// the type filter is covered by web/e2e/board.spec.ts.
//
// T1 adds the cross-lane persistence half — what a drop actually writes — which
// is not a pure function, so those cases render the page and drive the drag
// handlers the DndContext would call.

function item(id: string, over: Partial<ProjectItem> = {}): ProjectItem {
  return {
    id, space_id: 's1', number: Number(id) || 1, item_key: `VEC-${id}`,
    title: `Item ${id}`, description: '', kind: 'task', status: 'open',
    priority: 'medium', assignee_id: null, reporter_id: 'u1', sprint_id: 's',
    rank: '', due_at: null, created_at: '', updated_at: '',
    ...over,
  };
}

const memberName = (id: string | null | undefined) =>
  id === 'u-ana' ? 'Ana' : id === 'u-bo' ? 'Bo' : undefined;

const typeOptions = [
  { slug: 'bug', name: 'Bug' },
  { slug: 'task', name: 'Task' },
];

const columns = [
  { id: 'c-todo', label: 'To Do', color: '', statuses: ['open', 'todo'], dropStatus: 'open', wipLimit: null },
  { id: 'c-done', label: 'Done', color: '', statuses: ['done'], dropStatus: 'done', wipLimit: 2 },
];

const columnIdFor = (status: string) =>
  columns.find((c) => c.statuses.includes(status))?.id ?? columns[0].id;

describe('buildLanes', () => {
  it('returns a single unlabelled lane when grouping is off', () => {
    const items = [item('1'), item('2')];
    const lanes = buildLanes(items, 'none', memberName, typeOptions);
    expect(lanes).toHaveLength(1);
    expect(lanes[0].items).toHaveLength(2);
  });

  it('groups by assignee and names each lane for the member', () => {
    const items = [
      item('1', { assignee_id: 'u-ana' }),
      item('2', { assignee_id: 'u-bo' }),
      item('3', { assignee_id: 'u-ana' }),
    ];
    const lanes = buildLanes(items, 'assignee', memberName, typeOptions);

    expect(lanes.map((l) => l.label)).toEqual(['Ana', 'Bo']);
    expect(lanes[0].items).toHaveLength(2);
  });

  it('gives unassigned work an explicit lane, placed last', () => {
    // The requirement is that it is explicit, not hidden: an item with no
    // assignee must still be on the board somewhere a user can see.
    const items = [item('1', { assignee_id: 'u-ana' }), item('2', { assignee_id: null })];
    const lanes = buildLanes(items, 'assignee', memberName, typeOptions);

    expect(lanes).toHaveLength(2);
    expect(lanes[1].label).toBe('Unassigned');
    expect(lanes[1].items.map((i) => i.id)).toEqual(['2']);
  });

  it('gives typeless work an explicit lane too', () => {
    const items = [item('1', { kind: 'bug' }), item('2', { kind: '' })];
    const lanes = buildLanes(items, 'type', memberName, typeOptions);

    expect(lanes.map((l) => l.label)).toEqual(['Bug', 'No type']);
    expect(lanes[1].items.map((i) => i.id)).toEqual(['2']);
  });

  it('labels a type lane with the type name, not its slug', () => {
    const lanes = buildLanes([item('1', { kind: 'bug' })], 'type', memberName, typeOptions);
    expect(lanes[0].label).toBe('Bug');
  });

  it('falls back to the slug for a type the org list does not carry', () => {
    const lanes = buildLanes([item('1', { kind: 'spike' })], 'type', memberName, typeOptions);
    expect(lanes[0].label).toBe('spike');
  });

  it('never drops an item: every lane layout accounts for all of them', () => {
    const items = [
      item('1', { assignee_id: 'u-ana' }),
      item('2', { assignee_id: null }),
      item('3', { assignee_id: 'u-unknown' }),
    ];
    for (const mode of ['none', 'assignee', 'type'] as const) {
      const total = buildLanes(items, mode, memberName, typeOptions)
        .reduce((n, l) => n + l.items.length, 0);
      expect(total, `mode ${mode} lost items`).toBe(items.length);
    }
  });
});

describe('laneKeyOf', () => {
  it('maps a missing attribute to the catch-all lane', () => {
    expect(laneKeyOf(item('1', { assignee_id: null }), 'assignee')).toBe('__none__');
    expect(laneKeyOf(item('1', { kind: '' }), 'type')).toBe('__none__');
  });

  it('maps a present attribute to its own lane', () => {
    expect(laneKeyOf(item('1', { assignee_id: 'u-ana' }), 'assignee')).toBe('u-ana');
    expect(laneKeyOf(item('1', { kind: 'bug' }), 'type')).toBe('bug');
  });
});

describe('resolveDropTarget', () => {
  const items = [item('1', { status: 'open', assignee_id: 'u-ana' })];

  it('resolves a plain column drop', () => {
    expect(resolveDropTarget('c-done', items, columns, columnIdFor, 'none'))
      .toEqual({ columnId: 'c-done', laneId: undefined });
  });

  it('resolves a laned column drop to both axes', () => {
    expect(resolveDropTarget('c-done::u-bo', items, columns, columnIdFor, 'assignee'))
      .toEqual({ columnId: 'c-done', laneId: 'u-bo' });
  });

  it('resolves the catch-all lane as a real target', () => {
    // Dropping into "Unassigned" means unassign — a distinct outcome from
    // dropping nowhere.
    expect(resolveDropTarget('c-done::__none__', items, columns, columnIdFor, 'assignee'))
      .toEqual({ columnId: 'c-done', laneId: '__none__' });
  });

  it('resolves a drop onto another card to that card\'s column and lane', () => {
    // Without the lane half, dragging onto a card in another lane would move
    // the item's column but silently leave it in its old lane.
    const target = resolveDropTarget('1', items, columns, columnIdFor, 'assignee');
    expect(target).toEqual({ columnId: 'c-todo', laneId: 'u-ana' });
  });

  it('omits the lane when the board is not laned', () => {
    expect(resolveDropTarget('1', items, columns, columnIdFor, 'none'))
      .toEqual({ columnId: 'c-todo', laneId: undefined });
  });

  it('returns null for an unknown drop target', () => {
    expect(resolveDropTarget('nope', items, columns, columnIdFor, 'none')).toBeNull();
  });

  it('maps a card by its status, not by assuming the status is the column id', () => {
    // A configured column collects several statuses and carries its own id, so
    // "the column named by the status" is not a thing any more.
    const todoItem = item('9', { status: 'todo' });
    const target = resolveDropTarget('9', [todoItem], columns, columnIdFor, 'none');
    expect(target).toEqual({ columnId: 'c-todo', laneId: undefined });
  });
});

// ---------------------------------------------------------------------------
// T1: what a cross-lane drop writes
// ---------------------------------------------------------------------------
//
// The pure functions above decide *where* a drop landed. These cases pin what
// the board then sends, which is the half that changed in T1: a cross-lane drop
// under "by type" used to be a deliberate no-op because kind was not editable
// through the item PATCH contract. It is now, so the drop must persist — except
// into the catch-all "No type" lane, which no request can express because kind
// is NOT NULL and must name an active type.
//
// The vi.mock calls below are hoisted above the imports at the top of this file.

const SPRINT_ID = '33333333-3333-3333-3333-333333333333';

// Every card here sits in the "open" column except the last, so a drop onto a
// card is a pure lane change unless the fixture deliberately says otherwise.
const boardItems: ProjectItem[] = [
  item('1', { title: 'Dragged card', kind: 'task', status: 'open', assignee_id: null }),
  item('2', { title: 'Bug card', kind: 'bug', status: 'open', assignee_id: 'u-ana' }),
  item('3', { title: 'Typeless card', kind: '', status: 'open', assignee_id: null }),
  item('4', { title: 'Sibling card', kind: 'task', status: 'open', assignee_id: null }),
  item('5', { title: 'Done bug card', kind: 'bug', status: 'done', assignee_id: null }),
];

/** Every mutateAsync the board issued, with the item the mutation was bound to. */
const updateCalls: { spaceId: string; itemId: string; req: Record<string, unknown> }[] = [];
/** Controls the outcome of the PATCH; the recording above happens either way. */
const updateMutate = vi.fn();
const transitionMock = vi.fn();

/** The onDragStart/onDragEnd the board handed its DndContext, latest render. */
const dnd: {
  onDragStart?: (event: DragStartEvent) => void;
  onDragEnd?: (event: DragEndEvent) => void | Promise<void>;
} = {};

vi.mock('@dnd-kit/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@dnd-kit/core')>();
  return {
    ...actual,
    // dnd-kit's pointer machinery is not what is under test — the board's own
    // drop handling is. This double hands the test the exact callbacks the real
    // DndContext would invoke and leaves useDroppable/useSortable real.
    DndContext: ({ children, onDragStart, onDragEnd }: {
      children?: ReactNode;
      onDragStart?: (event: DragStartEvent) => void;
      onDragEnd?: (event: DragEndEvent) => void;
    }) => {
      dnd.onDragStart = onDragStart;
      dnd.onDragEnd = onDragEnd;
      return <>{children}</>;
    },
    // The floating drag preview would duplicate the dragged card's title in the
    // DOM, and lane placement is exactly what these cases assert on.
    DragOverlay: () => null,
  };
});

vi.mock('../../../lib/auth', () => ({ getCurrentOrgId: () => 'org1' }));

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useSpace: () => ({ data: { id: 's1', key: 'VEC' } }),
    useMe: () => ({ data: { org_id: 'org1' } }),
    useMembers: () => ({
      data: [{ user_id: 'u-ana', org_id: 'org1', display_name: 'Ana', email: 'ana@example.com', role: 'member' }],
    }),
    useActiveSprint: () => ({ data: { id: SPRINT_ID, name: 'Sprint One' }, isLoading: false }),
    useSprintItems: () => ({ data: boardItems, isLoading: false, error: null }),
    // No saved board config and no workflow states: the board falls back to its
    // status columns, whose ids are the statuses themselves.
    useWorkflowStates: () => ({ data: [] }),
    useBoardConfig: () => ({ data: { customized: false, columns: [] } }),
    useItemTypes: () => ({
      data: [
        { id: 't1', org_id: 'org1', slug: 'bug', name: 'Bug', position: 1, archived_at: null, created_at: '', updated_at: '' },
        { id: 't2', org_id: 'org1', slug: 'task', name: 'Task', position: 2, archived_at: null, created_at: '', updated_at: '' },
      ],
    }),
    // Records the item the PATCH was aimed at alongside its body, so a PATCH
    // aimed at the wrong item — or at no item at all — cannot pass as a PATCH
    // with the right body. That is not hypothetical: the board used to route
    // this through useUpdateProjectItem, which binds its item id at render
    // time, and the drag handler clears the active item before firing, so
    // every lane PATCH went to `.../items/` with an empty id and 404'd. The
    // itemId in these assertions is the thing that regressed.
    updateProjectItem: (spaceId: string, itemId: string, req: Record<string, unknown>) => {
      updateCalls.push({ spaceId, itemId, req });
      return updateMutate(req);
    },
    transitionProjectItemStatus: (spaceId: string, itemId: string, status: string) =>
      transitionMock(spaceId, itemId, status),
  };
});

function renderBoard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/vector/s1/board']}>
        <Routes>
          <Route path="/vector/:spaceId/board" element={<SprintBoardPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return client;
}

function groupBy(label: 'No lanes' | 'By assignee' | 'By type') {
  fireEvent.click(screen.getByRole('radio', { name: label }));
}

/**
 * Drops one card onto another, the way dnd-kit reports it. Drag start runs
 * first because the board binds its update mutation to the item being dragged.
 */
async function dragOnto(draggedId: string, overId: string) {
  await act(async () => {
    await dnd.onDragStart?.({ active: { id: draggedId } } as unknown as DragStartEvent);
  });
  await act(async () => {
    await dnd.onDragEnd?.(
      { active: { id: draggedId }, over: { id: overId } } as unknown as DragEndEvent,
    );
  });
}

/** The lane a card is currently rendered in, by its title. */
function laneOf(title: string): string | null {
  const card = screen.getByText(title);
  return card.closest('[data-testid="board-lane"]')?.getAttribute('data-lane-id') ?? null;
}

beforeEach(() => {
  updateCalls.length = 0;
  updateMutate.mockReset().mockResolvedValue(undefined);
  transitionMock.mockReset().mockResolvedValue(undefined);
});

describe('SprintBoardPage cross-lane drops', () => {
  it('persists the new type when a card is dragged into another type lane', async () => {
    // Breaks if the "by type" branch goes back to being a no-op, if it sends
    // anything but the target lane's slug, or if it aims at the wrong item.
    renderBoard();
    groupBy('By type');
    expect(laneOf('Dragged card')).toBe('task');

    await dragOnto('1', '2');

    expect(updateCalls).toEqual([{ spaceId: 's1', itemId: '1', req: { kind: 'bug' } }]);
    // Both cards sit in the same column, so nothing should have touched status.
    expect(transitionMock).not.toHaveBeenCalled();
  });

  it('sends nothing when a card is dropped into the catch-all typeless lane', async () => {
    // Breaks if the null laneValue is passed through as kind: '' — the column
    // is NOT NULL and must name an active type, so the backend 400s — and
    // breaks if the optimistic override survives the drop instead of snapping
    // the card back to the type it still has.
    renderBoard();
    groupBy('By type');

    await dragOnto('1', '3');

    expect(updateCalls).toEqual([]);
    expect(transitionMock).not.toHaveBeenCalled();
    expect(laneOf('Dragged card')).toBe('task');
  });

  it('still persists the new assignee when a card is dragged into another assignee lane', async () => {
    // Regression guard on the axis that already worked: the type branch must be
    // an addition, not a replacement.
    renderBoard();
    groupBy('By assignee');

    await dragOnto('1', '2');

    expect(updateCalls).toEqual([{ spaceId: 's1', itemId: '1', req: { assignee_id: 'u-ana' } }]);
    expect(transitionMock).not.toHaveBeenCalled();
  });

  it('unassigns on a drop into the catch-all assignee lane', async () => {
    // The contrast that justifies the special case above: null is a real value
    // for assignee_id and means unassign, so the catch-all lane is a live
    // target here. Breaks if the type lane's no-op is generalised to both axes.
    renderBoard();
    groupBy('By assignee');

    await dragOnto('2', '1');

    expect(updateCalls).toEqual([{ spaceId: 's1', itemId: '2', req: { assignee_id: null } }]);
  });

  it('does nothing when a drop changes neither column nor lane', async () => {
    // Breaks if the handler stops comparing the drop target against where the
    // card already is and writes on every drop.
    renderBoard();
    groupBy('By type');

    await dragOnto('1', '4');

    expect(updateCalls).toEqual([]);
    expect(transitionMock).not.toHaveBeenCalled();
  });

  it('writes both axes when a drop changes column and type at once', async () => {
    // Breaks if the type PATCH is made mutually exclusive with the status
    // transition — one drop can carry both, and dropping half of it silently
    // is the failure this whole surface exists to avoid.
    renderBoard();
    groupBy('By type');

    await dragOnto('1', '5');

    expect(transitionMock).toHaveBeenCalledWith('s1', '1', 'done');
    expect(updateCalls).toEqual([{ spaceId: 's1', itemId: '1', req: { kind: 'bug' } }]);
  });

  it('leaves a card in its stored type lane when the type PATCH is rejected', async () => {
    // Breaks if the optimistic lane override is only rolled back on success:
    // the card would keep showing the type it was dragged to while the server
    // still holds the old one. Also pins the refetch, which is the only way the
    // board learns which half of a two-write drop actually took.
    updateMutate.mockRejectedValueOnce(new Error('unknown item type'));
    const client = renderBoard();
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    groupBy('By type');

    await dragOnto('1', '2');

    expect(updateCalls).toEqual([{ spaceId: 's1', itemId: '1', req: { kind: 'bug' } }]);
    expect(laneOf('Dragged card')).toBe('task');
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.sprintItems('s1', SPRINT_ID) });
  });
});
