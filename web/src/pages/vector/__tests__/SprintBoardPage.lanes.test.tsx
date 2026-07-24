import { describe, expect, it } from 'vitest';
import { buildLanes, resolveDropTarget, laneKeyOf } from '../SprintBoardPage';
import type { ProjectItem } from '../../../lib/api';

// W4: swimlanes and drop resolution. These are the pure pieces of the board's
// grouping and drag logic; the rendered interplay of lanes, columns, WIP and
// the type filter is covered by web/e2e/board.spec.ts.

function item(id: string, over: Partial<ProjectItem> = {}): ProjectItem {
  return {
    id, space_id: 's1', number: Number(id) || 1, item_key: `VEC-${id}`,
    title: `Item ${id}`, description: '', kind: 'task', status: 'open',
    priority: 'medium', assignee_id: null, reporter_id: 'u1', sprint_id: 's',
    rank: '', labels: [], created_at: '', updated_at: '',
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
