import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PortalSection } from '../PortalSection';
import type { AdminPortalConfig, APIError } from '../../../lib/api';

// A1: the agent-facing half of the customer portal. The section has three
// states — no portal yet (404 → offer to create), configured, forbidden
// (403) — and the configured view's copy control must hand an agent the FULL
// customer URL, because the bare key means nothing without the /portal/ path.

const createMock = vi.fn().mockResolvedValue(undefined);
const updateMock = vi.fn().mockResolvedValue(undefined);

let queryState: {
  data: AdminPortalConfig | undefined;
  isLoading: boolean;
  error: Partial<APIError> | null;
};

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    usePortalConfig: () => queryState,
    useCreatePortal: () => ({ mutateAsync: createMock, isPending: false }),
    useUpdatePortal: () => ({ mutateAsync: updateMock, isPending: false }),
  };
});

const configured: AdminPortalConfig = {
  portal_key: 'k3y0fth3p0rtal99abcd',
  name: 'Acme Support',
  intro: 'How can we help?',
  enabled: true,
  portal_id: '99999999-9999-9999-9999-999999999999',
};

beforeEach(() => {
  createMock.mockClear().mockResolvedValue(undefined);
  updateMock.mockClear().mockResolvedValue(undefined);
  queryState = { data: configured, isLoading: false, error: null };
});

afterEach(() => vi.clearAllMocks());

function renderSection() {
  return render(<PortalSection orgId="org-1" spaceId="space-1" />);
}

describe('PortalSection states', () => {
  it('offers to create a portal when the config GET 404s', () => {
    queryState = {
      data: undefined,
      isLoading: false,
      error: { status: 404, code: 'NOT_FOUND', message: 'this space has no customer portal' },
    };
    renderSection();

    expect(screen.getByTestId('portal-create')).toBeInTheDocument();
    expect(screen.getByTestId('portal-create-name')).toBeInTheDocument();
    // The 404 is the "no portal yet" signal, not an error state — no error
    // copy, and none of the configured view leaks through.
    expect(screen.queryByTestId('portal-configured')).not.toBeInTheDocument();
    expect(screen.queryByTestId('portal-config-url')).not.toBeInTheDocument();
  });

  it('requires a name before the create button enables', () => {
    queryState = {
      data: undefined,
      isLoading: false,
      error: { status: 404, code: 'NOT_FOUND', message: 'this space has no customer portal' },
    };
    renderSection();

    expect(screen.getByTestId('portal-create-button')).toBeDisabled();
    fireEvent.change(screen.getByTestId('portal-create-name'), { target: { value: '   ' } });
    expect(screen.getByTestId('portal-create-button')).toBeDisabled();
    fireEvent.change(screen.getByTestId('portal-create-name'), { target: { value: 'Acme Support' } });
    expect(screen.getByTestId('portal-create-button')).toBeEnabled();
  });

  it('renders the configured view with the key, the URL, and the editable fields', () => {
    renderSection();

    expect(screen.getByTestId('portal-configured')).toBeInTheDocument();
    expect(screen.getByTestId('portal-config-key')).toHaveTextContent(configured.portal_key);
    expect(screen.getByTestId('portal-config-url')).toHaveTextContent(
      `${window.location.origin}/portal/${configured.portal_key}`,
    );
    expect(screen.getByTestId('portal-config-name')).toHaveValue('Acme Support');
    expect(screen.getByTestId('portal-config-intro')).toHaveValue('How can we help?');
    expect(screen.getByTestId('portal-config-state')).toHaveTextContent('The portal is live');
    expect(screen.getByTestId('portal-config-toggle')).toHaveTextContent('Disable');
    expect(screen.queryByTestId('portal-create')).not.toBeInTheDocument();
  });

  it('renders the forbidden state on 403 and none of the configuration', () => {
    queryState = {
      data: undefined,
      isLoading: false,
      error: { status: 403, code: 'FORBIDDEN', message: 'insufficient permissions' },
    };
    renderSection();

    expect(screen.getByTestId('portal-forbidden')).toHaveTextContent(
      'You need space admin to manage the customer portal.',
    );
    // A member without manage_space sees this line and nothing else — not the
    // create affordance, not a raw error, and no portal key anywhere.
    expect(screen.queryByTestId('portal-create')).not.toBeInTheDocument();
    expect(screen.queryByTestId('portal-configured')).not.toBeInTheDocument();
    expect(screen.queryByTestId('portal-config-url')).not.toBeInTheDocument();
  });
});

describe('PortalSection copy control', () => {
  it('copies the full customer URL, not the bare key', () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });

    renderSection();
    fireEvent.click(screen.getByTestId('portal-config-copy'));

    expect(writeText).toHaveBeenCalledTimes(1);
    const copiedValue = writeText.mock.calls[0][0] as string;
    expect(copiedValue).toBe(`${window.location.origin}/portal/${configured.portal_key}`);
    // The bare key would paste into an email as a meaningless string — the
    // assertion that it is a URL is the point of the control.
    expect(copiedValue).not.toBe(configured.portal_key);
    expect(screen.getByTestId('portal-config-copy')).toHaveTextContent('Copied');
  });
});

describe('PortalSection sends three-state-honest requests', () => {
  it('sends enabled ALONE from the toggle — a toggle must not resend text fields', async () => {
    renderSection();
    fireEvent.click(screen.getByTestId('portal-config-toggle'));

    await vi.waitFor(() => expect(updateMock).toHaveBeenCalled());
    // Exactly {enabled: false}: no name or intro keys at all, so the server's
    // absent-means-leave-alone contract does the rest.
    expect(updateMock).toHaveBeenCalledWith({ enabled: false });
  });

  it('sends only name and intro from a save — a rename must not carry enabled', async () => {
    renderSection();

    fireEvent.change(screen.getByTestId('portal-config-name'), {
      target: { value: 'Aurora Helpdesk' },
    });
    const save = screen.getByTestId('portal-config-save');
    expect(save).toBeEnabled();
    fireEvent.click(save);

    await vi.waitFor(() => expect(updateMock).toHaveBeenCalled());
    expect(updateMock).toHaveBeenCalledWith({ name: 'Aurora Helpdesk', intro: 'How can we help?' });
    const sent = updateMock.mock.calls[0][0] as Record<string, unknown>;
    expect('enabled' in sent).toBe(false);
  });

  it('cannot save until something changes, and never with an empty name', () => {
    renderSection();
    expect(screen.getByTestId('portal-config-save')).toBeDisabled();

    fireEvent.change(screen.getByTestId('portal-config-name'), { target: { value: '  ' } });
    expect(screen.getByTestId('portal-config-save')).toBeDisabled();

    fireEvent.change(screen.getByTestId('portal-config-name'), { target: { value: 'Renamed' } });
    expect(screen.getByTestId('portal-config-save')).toBeEnabled();
  });
});
