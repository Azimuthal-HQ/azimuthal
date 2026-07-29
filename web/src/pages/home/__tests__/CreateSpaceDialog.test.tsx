import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../../../lib/api';
import { CreateSpaceDialog } from '../CreateSpaceDialog';

// Migrated from HomeOverviewPage.test.tsx, deliberately rather than silently.
//
// P5 replaced the interim Home with a dashboard, and its two STAT-CARD
// regression tests went with the cards they pinned — the counts they asserted
// are not a thing the product shows any more, and a skipped test would have
// claimed coverage of a feature that no longer exists.
//
// These two came WITH the dialog. Both were written for real shipped defects
// in how a failed space creation surfaces, the dialog still does exactly that
// job, and it now lives in its own file so the page hosting it can change
// again without moving them a second time.
//
// Partial mock: the hooks are stubbed, but APIError and friendlyErrorMessage
// stay real — the point of these tests is that the dialog routes failures
// through the real friendlyErrorMessage (P2.5 W5), so mocking it would prove
// nothing.
const useCreateSpaceMock = vi.fn();

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useCreateSpace: (orgId: string) => useCreateSpaceMock(orgId),
  };
});

function mutationState(error: unknown) {
  return { error, isPending: false, mutateAsync: vi.fn() };
}

function renderDialog() {
  return render(
    <MemoryRouter>
      <CreateSpaceDialog open onOpenChange={vi.fn()} orgId="org-1" onCreated={vi.fn()} />
    </MemoryRouter>,
  );
}

describe('CreateSpaceDialog error surfacing', () => {
  beforeEach(() => {
    useCreateSpaceMock.mockReset();
  });

  it('passes a CONFLICT message through verbatim — a slug taken in this module', () => {
    useCreateSpaceMock.mockReturnValue(
      mutationState(
        new APIError(409, {
          error: {
            code: 'CONFLICT',
            message: 'a Vector space with this slug already exists in the organization',
            request_id: 'req-1',
          },
        }),
      ),
    );
    renderDialog();

    expect(
      screen.getByText('a Vector space with this slug already exists in the organization'),
    ).toBeInTheDocument();
  });

  it('collapses non-human codes to the fallback — raw backend strings never render', () => {
    useCreateSpaceMock.mockReturnValue(
      mutationState(
        new APIError(400, {
          error: { code: 'BAD_REQUEST', message: 'invalid request body', request_id: 'req-2' },
        }),
      ),
    );
    renderDialog();

    expect(screen.getByText('The space could not be created.')).toBeInTheDocument();
    expect(screen.queryByText('invalid request body')).toBeNull();
  });

  it('offers every module as a space type — regression: Codex was once omitted', () => {
    useCreateSpaceMock.mockReturnValue(mutationState(null));
    renderDialog();

    // The interim Home's stat row shipped without Codex. The cards are gone,
    // but "every module is offered" is still a live property of this dialog.
    expect(screen.getByRole('button', { name: /beacon/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /codex/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /vector/i })).toBeInTheDocument();
  });

  it('requires a ticket abbreviation for a Beacon space and not for the others', () => {
    useCreateSpaceMock.mockReturnValue(mutationState(null));
    renderDialog();

    // Beacon is the default type, so the abbreviation field is present and
    // the submit is blocked until it is filled.
    expect(screen.getByLabelText(/ticket abbreviation/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /create space/i })).toBeDisabled();
  });
});
