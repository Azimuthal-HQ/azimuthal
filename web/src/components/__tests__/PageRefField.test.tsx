import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { PageRefField } from '../PageRefField';

// A4. The page picker resolves to a SELECTION — a relation target is a real
// entity's (type, id), so unlike TicketRefField free text has nowhere to go.
// These tests pin the two halves of that: the typeahead queries and renders,
// and only an explicit pick reaches onSelect.

const suggestionsHook = vi.fn();

vi.mock('../../lib/api', () => ({
  usePageSuggestions: (orgId: string, q: string) => suggestionsHook(orgId, q),
}));

const RUNBOOK = {
  page_id: 'p-1',
  title: 'Deployment runbook',
  space_id: 's-9',
  space_key: 'DOCS',
  space_name: 'Handbook',
};
const ROADMAP = {
  page_id: 'p-2',
  title: 'Roadmap notes',
  space_id: 's-9',
  space_key: 'DOCS',
  space_name: 'Handbook',
};

function matching(q: string) {
  const needle = q.trim().toLowerCase();
  if (!needle) return [];
  return [RUNBOOK, ROADMAP].filter((s) => s.title.toLowerCase().includes(needle));
}

const onSelect = vi.fn();

const input = () => screen.getByTestId('page-ref') as HTMLInputElement;

beforeEach(() => {
  onSelect.mockReset();
  suggestionsHook.mockReset();
  suggestionsHook.mockImplementation((orgId: string, q: string) => ({
    data: orgId ? matching(q) : [],
    isLoading: false,
  }));
});

describe('PageRefField', () => {
  it('queries on input and renders the suggestions', () => {
    render(<PageRefField orgId="org-1" onSelect={onSelect} />);

    fireEvent.focus(input());
    fireEvent.change(input(), { target: { value: 'runbook' } });

    // The hook is keyed on the org and the typed text — the React-Query cache
    // key IS the request, so this asserts the query fired for this input.
    expect(suggestionsHook).toHaveBeenCalledWith('org-1', 'runbook');
    expect(screen.getByTestId('page-ref-option-p-1')).toBeInTheDocument();
    expect(screen.getByText('Deployment runbook')).toBeInTheDocument();
    // The space context is how a human tells apart same-titled pages.
    expect(screen.getByText('DOCS · Handbook')).toBeInTheDocument();
  });

  it('does not query while the field is closed', () => {
    render(<PageRefField orgId="org-1" onSelect={onSelect} />);
    // Rendered but never focused: the hook must be called with a disabled
    // ('') org so a panel that merely shows the picker asks the server nothing.
    expect(suggestionsHook).toHaveBeenCalledWith('', '');
  });

  it('clicking a suggestion reports the selection and clears the input', () => {
    render(<PageRefField orgId="org-1" onSelect={onSelect} />);

    fireEvent.focus(input());
    fireEvent.change(input(), { target: { value: 'notes' } });
    fireEvent.click(screen.getByTestId('page-ref-option-p-2'));

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(ROADMAP);
    expect(input().value).toBe('');
  });

  it('Enter accepts only an explicitly highlighted suggestion', () => {
    render(<PageRefField orgId="org-1" onSelect={onSelect} />);

    fireEvent.focus(input());
    fireEvent.change(input(), { target: { value: 'runbook' } });

    // No highlight: Enter must NOT guess the first row — a relation target
    // has to be chosen, not inferred.
    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onSelect).not.toHaveBeenCalled();

    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith(RUNBOOK);
  });

  it('typing free text alone never selects', () => {
    render(<PageRefField orgId="org-1" onSelect={onSelect} />);

    fireEvent.focus(input());
    fireEvent.change(input(), { target: { value: 'a page nobody made' } });
    fireEvent.keyDown(input(), { key: 'Enter' });

    expect(onSelect).not.toHaveBeenCalled();
  });
});
