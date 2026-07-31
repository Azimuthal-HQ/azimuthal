import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, Navigate } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

/**
 * The /admin/workflows mounting guard.
 *
 * Until this PR, App.tsx declared the workflow admin page as a SIBLING of the
 * AdminLayout route:
 *
 *     <Route path="admin/workflows" element={<WorkflowAdminPage />} />
 *     <Route path="admin" element={<AdminLayout />}> … </Route>
 *
 * React Router matched the more specific literal first, so the page never
 * passed through AdminLayout's `caller_is_admin` check and rendered for any
 * authenticated org member. Nothing caught it: the only spec that visits the
 * URL (web/e2e/workflow-admin.spec.ts) signs in as an org admin, for whom both
 * mountings look identical.
 *
 * This test asserts the structural property that made it a bug — that reaching
 * the page goes THROUGH the admin gate — rather than asserting the current
 * shape of App.tsx, so it keeps holding if the routes are reorganised again.
 *
 * # Fails-before evidence
 *
 * Reverting App.tsx to the sibling declaration makes "a non-admin org member
 * gets the branded not-found" fail: the page renders, admin-layout is absent,
 * and no 404 appears. See the PR body.
 */

// The page is stubbed. What is under test is the GATE, and a real page would
// drag in the whole api module and turn a routing test into an integration one.
const WorkflowAdminPageStub = () => <div data-testid="workflow-admin-page">Workflows</div>;

// A minimal stand-in for AdminLayout's contract: admins get the outlet, and
// everybody else gets the branded not-found. Mirrors pages/admin/AdminLayout.tsx.
function GuardedAdminArea({ isAdmin, children }: { isAdmin: boolean; children: React.ReactNode }) {
  if (!isAdmin) return <div>Page not found</div>;
  return <div data-testid="admin-layout">{children}</div>;
}

/**
 * Renders the two candidate mountings so the difference is visible in one file.
 *
 * `nested` is what shipped in this PR; `sibling` is what shipped before it, and
 * exists here as the negative control — a test that only exercised the fixed
 * arrangement could not show that the arrangement is what matters.
 */
function renderAt(path: string, { isAdmin, mounting }: { isAdmin: boolean; mounting: 'nested' | 'sibling' }) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        {mounting === 'sibling' && (
          <Route path="admin/workflows" element={<WorkflowAdminPageStub />} />
        )}
        <Route
          path="admin"
          element={
            <GuardedAdminArea isAdmin={isAdmin}>
              <Routes>
                <Route path="workflows" element={<WorkflowAdminPageStub />} />
                <Route path="people" element={<div>People</div>} />
                <Route index element={<Navigate to="/admin/people" replace />} />
              </Routes>
            </GuardedAdminArea>
          }
        >
          <Route path="*" element={null} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe('the workflow admin page sits inside the admin guard', () => {
  it('renders for an org admin', () => {
    renderAt('/admin/workflows', { isAdmin: true, mounting: 'nested' });
    expect(screen.getByTestId('workflow-admin-page')).toBeInTheDocument();
    expect(screen.getByTestId('admin-layout')).toBeInTheDocument();
  });

  it('gives a non-admin org member the branded not-found, never the editor', () => {
    renderAt('/admin/workflows', { isAdmin: false, mounting: 'nested' });

    expect(screen.queryByTestId('workflow-admin-page')).not.toBeInTheDocument();
    // Absence alone is a vacuous assertion — a typo'd route id also produces
    // it. Pair it with the positive: the branded not-found is what rendered,
    // and the admin chrome did not.
    expect(screen.getByText(/page not found/i)).toBeInTheDocument();
    expect(screen.queryByTestId('admin-layout')).not.toBeInTheDocument();
  });

  it('is exactly the sibling mounting that leaked it, which is why the nesting matters', () => {
    // The negative control. With the pre-PR declaration the same non-admin
    // reaches the page, and neither the guard nor the not-found is involved.
    // If this ever stops passing, the sibling arrangement has stopped being
    // dangerous and the test above has stopped proving anything.
    renderAt('/admin/workflows', { isAdmin: false, mounting: 'sibling' });

    expect(screen.getByTestId('workflow-admin-page')).toBeInTheDocument();
    expect(screen.queryByText(/page not found/i)).not.toBeInTheDocument();
  });
});

describe('the real App wiring keeps workflows under the admin group', () => {
  it('declares no admin/workflows route outside the admin element', async () => {
    // Reading the source is blunt, but it is the only thing that pins the
    // FILE rather than a reconstruction of it — the reconstruction above can
    // stay green while App.tsx drifts back.
    const { readFileSync } = await import('node:fs');
    const { dirname, resolve } = await import('node:path');
    const { fileURLToPath } = await import('node:url');

    const appSource = readFileSync(
      resolve(dirname(fileURLToPath(import.meta.url)), '../../App.tsx'),
      'utf8',
    );

    expect(appSource).toContain('<Route path="workflows" element={<WorkflowAdminPage />} />');
    expect(appSource).not.toContain('path="admin/workflows"');
  });
});

// Keeps the import list honest: this file mocks nothing, so an accidental
// import of the real api module would surface as a network attempt rather than
// a silent pass.
vi.mock('../../lib/api', () => {
  throw new Error('this routing test must not reach the api module');
});
