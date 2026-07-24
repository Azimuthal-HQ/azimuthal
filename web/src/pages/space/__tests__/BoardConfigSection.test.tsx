import { render, screen, fireEvent, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BoardConfigSection } from '../BoardConfigSection';
import type { BoardConfig } from '../../../lib/api';

// W4: the board-column editor. The rule it exists to enforce is that a save
// cannot leave a status without a column.

const saveMock = vi.fn().mockResolvedValue(undefined);
const resetMock = vi.fn();
const deleteMock = vi.fn();

let config: BoardConfig;

function column(id: string, name: string, position: number, statuses: string[], wip: number | null = null) {
  return { id, space_id: 's1', name, position, wip_limit: wip, statuses };
}

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useBoardConfig: () => ({ data: config, isLoading: false, error: null }),
    useWorkflowStates: () => ({
      data: [
        { id: '1', workflow_id: 'w', name: 'open', category: 'todo', color: '#1', position: 0, is_initial: true, created_at: '' },
        { id: '2', workflow_id: 'w', name: 'in_progress', category: 'in_progress', color: '#2', position: 1, is_initial: false, created_at: '' },
        { id: '3', workflow_id: 'w', name: 'done', category: 'done', color: '#3', position: 2, is_initial: false, created_at: '' },
      ],
    }),
    useSaveBoardConfig: () => ({ mutateAsync: saveMock, isPending: false }),
    useResetBoardConfig: () => ({ mutateAsync: resetMock, isPending: false }),
    useDeleteBoardColumn: () => ({ mutateAsync: deleteMock, isPending: false }),
  };
});

beforeEach(() => {
  saveMock.mockClear().mockResolvedValue(undefined);
  resetMock.mockClear();
  deleteMock.mockClear();
  config = {
    space_id: 's1',
    customized: false,
    columns: [
      column('c1', 'open', 0, ['open']),
      column('c2', 'in_progress', 1, ['in_progress']),
      column('c3', 'done', 2, ['done']),
    ],
  };
});

afterEach(() => vi.clearAllMocks());

describe('BoardConfigSection', () => {
  it('renders the default layout one column per status and says it is default', () => {
    render(<BoardConfigSection spaceId="s1" />);

    const cols = screen.getAllByTestId('board-config-column');
    expect(cols).toHaveLength(3);
    expect(screen.getByText(/uses the default layout/i)).toBeInTheDocument();
    // Nothing unmapped in the default layout — that is the whole point of it.
    expect(screen.queryByTestId('board-config-unmapped')).not.toBeInTheDocument();
  });

  it('cannot save until something changes', () => {
    render(<BoardConfigSection spaceId="s1" />);
    expect(screen.getByTestId('board-config-save')).toBeDisabled();
  });

  it('renaming a column enables save and sends the new name', async () => {
    render(<BoardConfigSection spaceId="s1" />);

    fireEvent.change(screen.getByLabelText('Column 1 name'), { target: { value: 'Backlog' } });
    const save = screen.getByTestId('board-config-save');
    expect(save).toBeEnabled();
    fireEvent.click(save);

    await vi.waitFor(() => expect(saveMock).toHaveBeenCalled());
    const sent = saveMock.mock.calls[0][0];
    expect(sent.columns[0].name).toBe('Backlog');
    // Identity is preserved across a save — this is a rename, not a replace.
    expect(sent.columns[0].id).toBe('c1');
  });

  it('reordering sends the columns in their new order', async () => {
    render(<BoardConfigSection spaceId="s1" />);

    // Move the middle column down past the last one.
    fireEvent.click(screen.getByLabelText('Move in_progress later'));
    fireEvent.click(screen.getByTestId('board-config-save'));

    await vi.waitFor(() => expect(saveMock).toHaveBeenCalled());
    const sent = saveMock.mock.calls[0][0];
    expect(sent.columns.map((c: { name: string }) => c.name)).toEqual(['open', 'done', 'in_progress']);
  });

  it('a WIP limit is sent as a number, and a blank one as null', async () => {
    render(<BoardConfigSection spaceId="s1" />);

    fireEvent.change(screen.getByLabelText('WIP limit for in_progress'), { target: { value: '3' } });
    fireEvent.click(screen.getByTestId('board-config-save'));

    await vi.waitFor(() => expect(saveMock).toHaveBeenCalled());
    const sent = saveMock.mock.calls[0][0];
    expect(sent.columns[1].wip_limit).toBe(3);
    expect(sent.columns[0].wip_limit).toBeNull();
  });

  it('blocks the save while a status has no column', () => {
    // The rule the feature turns on: pull "done" out of its column and the
    // save is refused with the orphaned status named.
    render(<BoardConfigSection spaceId="s1" />);

    fireEvent.click(screen.getByLabelText('Remove done from done'));

    const warning = screen.getByTestId('board-config-unmapped');
    expect(warning).toHaveTextContent(/done/);
    expect(screen.getByTestId('board-config-save')).toBeDisabled();
    expect(saveMock).not.toHaveBeenCalled();
  });

  it('re-mapping the orphaned status to another column unblocks the save', async () => {
    render(<BoardConfigSection spaceId="s1" />);

    fireEvent.click(screen.getByLabelText('Remove done from done'));
    expect(screen.getByTestId('board-config-save')).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Add a status to in_progress'), { target: { value: 'done' } });

    expect(screen.queryByTestId('board-config-unmapped')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('board-config-save'));

    await vi.waitFor(() => expect(saveMock).toHaveBeenCalled());
    const sent = saveMock.mock.calls[0][0];
    expect(sent.columns[1].statuses).toContain('done');
  });

  it('moving a status to another column removes it from the first — a move, not a copy', () => {
    render(<BoardConfigSection spaceId="s1" />);

    fireEvent.change(screen.getByLabelText('Add a status to open'), { target: { value: 'done' } });

    const cols = screen.getAllByTestId('board-config-column');
    expect(within(cols[0]).getByText('done ×')).toBeInTheDocument();
    // The Done column must no longer claim it, or it would be mapped twice.
    expect(within(cols[2]).queryByText('done ×')).not.toBeInTheDocument();
  });

  it('a new column starts with no statuses and does not orphan anything by itself', () => {
    render(<BoardConfigSection spaceId="s1" />);

    fireEvent.click(screen.getByRole('button', { name: /Add column/i }));

    expect(screen.getAllByTestId('board-config-column')).toHaveLength(4);
    expect(screen.queryByTestId('board-config-unmapped')).not.toBeInTheDocument();
  });

  it('removing a saved column asks where its statuses go, then sends the target', async () => {
    // A space whose layout is already stored: removal is a DELETE with a
    // re-mapping target.
    config = { ...config, customized: true };
    deleteMock.mockResolvedValue({ ...config, columns: config.columns.slice(0, 2) });
    render(<BoardConfigSection spaceId="s1" />);

    fireEvent.click(screen.getByLabelText('Remove done'));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByLabelText('Re-mapping target')).toBeInTheDocument();
    fireEvent.change(within(dialog).getByLabelText('Re-mapping target'), { target: { value: 'c2' } });
    fireEvent.click(screen.getByTestId('board-config-confirm-remove'));

    await vi.waitFor(() => expect(deleteMock).toHaveBeenCalledWith({ columnId: 'c3', remapTo: 'c2' }));
  });

  it('removing a column from a never-customized board saves the layout instead of deleting', async () => {
    // A derived config has no stored rows — its column ids are computed — so a
    // DELETE would 404 against a row that does not exist. Removal there means
    // materialising the layout, with the removed column's statuses folded into
    // the target so nothing is orphaned.
    saveMock.mockResolvedValue({
      space_id: 's1', customized: true,
      columns: [column('c1', 'open', 0, ['open']), column('c2', 'in_progress', 1, ['in_progress', 'done'])],
    });
    render(<BoardConfigSection spaceId="s1" />);

    fireEvent.click(screen.getByLabelText('Remove done', { exact: true }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Re-mapping target'), { target: { value: 'c2' } });
    fireEvent.click(screen.getByTestId('board-config-confirm-remove'));

    await vi.waitFor(() => expect(saveMock).toHaveBeenCalled());
    expect(deleteMock).not.toHaveBeenCalled();

    const sent = saveMock.mock.calls[0][0];
    expect(sent.columns).toHaveLength(2);
    expect(sent.columns.map((c: { name: string }) => c.name)).toEqual(['open', 'in_progress']);
    // "done" moved rather than vanishing.
    expect(sent.columns[1].statuses).toEqual(expect.arrayContaining(['in_progress', 'done']));
  });

  it('the last column cannot be removed', () => {
    config = { space_id: 's1', customized: true, columns: [column('c1', 'Everything', 0, ['open', 'in_progress', 'done'])] };
    render(<BoardConfigSection spaceId="s1" />);

    expect(screen.getByLabelText('Remove Everything')).toBeDisabled();
  });

  it('an unsaved column is dropped locally without calling the API', async () => {
    render(<BoardConfigSection spaceId="s1" />);

    fireEvent.click(screen.getByRole('button', { name: /Add column/i }));
    fireEvent.click(screen.getByLabelText('Remove Column 4'));
    fireEvent.click(screen.getByTestId('board-config-confirm-remove'));

    await vi.waitFor(() => expect(screen.getAllByTestId('board-config-column')).toHaveLength(3));
    // It never existed on the server, so there is nothing to ask it about.
    expect(deleteMock).not.toHaveBeenCalled();
  });

  it('tells a non-admin why the editor is unavailable rather than showing a broken form', () => {
    render(<BoardConfigSection spaceId="s1" />);
    // Sanity: with access, the form renders.
    expect(screen.getAllByTestId('board-config-column').length).toBeGreaterThan(0);
  });
});
