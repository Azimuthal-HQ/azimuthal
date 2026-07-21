import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { AccessMatrixPage } from '../AccessMatrixPage';

// Fixture: parent team ⊃ child team; the CHILD holds the only grant on
// space-1. ADR-0007 subject-side expansion means the PARENT's cell renders
// ghosted (inherited) — and acting on it must offer to CREATE a direct
// grant, never to edit the child's row (failure mode 5).
const PARENT = { id: 'team-parent', parent_id: null, path: ['team-parent'], name: 'Engineering', is_default: false, member_count: 5 };
const CHILD = { id: 'team-child', parent_id: 'team-parent', path: ['team-parent', 'team-child'], name: 'Platform', is_default: false, member_count: 2 };
const SPACE = { id: 'space-1', name: 'Platform Space', type: 'vector', visibility: 'discoverable' };
const GRANT = { id: 'grant-1', team_id: 'team-child', space_id: 'space-1', role: 'viewer' };

const previewMutate = vi.fn();
const applyMutate = vi.fn();

vi.mock('../../../lib/api', () => ({
  friendlyErrorMessage: (_err: unknown, fallback: string) => fallback,
  useAccessMatrix: () => ({
    data: { teams: [PARENT, CHILD], spaces: [SPACE], grants: [GRANT] },
    isLoading: false,
    error: null,
  }),
  useBulkPreviewGrants: () => ({ mutate: previewMutate, isPending: false }),
  useBulkApplyGrants: () => ({ mutate: applyMutate, isPending: false }),
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'u1', email: 'admin@example.com', orgId: 'org-1', role: 'member' } }),
}));

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/admin/access']}>
      <AccessMatrixPage />
    </MemoryRouter>,
  );
}

describe('AccessMatrixPage', () => {
  it('renders direct, inherited, and empty cell states distinctly', () => {
    renderPage();
    const childCell = screen.getByTestId('matrix-cell-team-child-space-1');
    const parentCell = screen.getByTestId('matrix-cell-team-parent-space-1');
    expect(childCell).toHaveAttribute('data-state', 'direct');
    expect(parentCell).toHaveAttribute('data-state', 'inherited');
  });

  it('offers to CREATE a direct grant from a ghosted cell, never to edit the inherited one', () => {
    renderPage();
    fireEvent.click(screen.getByTestId('matrix-cell-team-parent-space-1'));

    // The editor names the descendant holding the grant and states that a
    // NEW direct grant will be created for the parent.
    const note = screen.getByTestId('matrix-inherited-note');
    expect(note.textContent).toContain('inherited from Platform');
    expect(note.textContent).toContain('creates a direct grant for Engineering');
    expect(note.textContent).toContain('never edited');

    // Staging a role targets the PARENT's cell — the change key carries the
    // parent team id, visible as the staged marker on the parent cell only.
    fireEvent.click(screen.getByTestId('matrix-editor-role-agent'));
    expect(screen.getByTestId('matrix-cell-team-parent-space-1')).toHaveAttribute('data-staged', 'true');
    expect(screen.getByTestId('matrix-cell-team-child-space-1')).not.toHaveAttribute('data-staged');
  });

  it('previews staged changes with the parent team id in the change set', () => {
    renderPage();
    fireEvent.click(screen.getByTestId('matrix-cell-team-parent-space-1'));
    fireEvent.click(screen.getByTestId('matrix-editor-role-agent'));
    fireEvent.click(screen.getByTestId('matrix-preview-button'));

    expect(previewMutate).toHaveBeenCalledTimes(1);
    const changes = previewMutate.mock.calls[0][0];
    expect(changes).toEqual([{ team_id: 'team-parent', space_id: 'space-1', role: 'agent' }]);
  });

  it('a revoke on a direct cell stages null', () => {
    renderPage();
    fireEvent.click(screen.getByTestId('matrix-cell-team-child-space-1'));
    fireEvent.click(screen.getByTestId('matrix-editor-revoke'));
    fireEvent.click(screen.getByTestId('matrix-preview-button'));
    const changes = previewMutate.mock.calls.at(-1)?.[0];
    expect(changes).toEqual([{ team_id: 'team-child', space_id: 'space-1', role: null }]);
  });
});
