import { useState } from 'react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { TicketRefField, type TicketRefFieldProps } from '../TicketRefField';

// A1. ticket_ref is an operator's note to the audit log, not a foreign key —
// the server stores whatever string arrives and validates only its length.
// The typeahead exists to save typing, and every test here defends the line
// between "assists" and "constrains": free text must survive untouched, at
// every step, whether or not the suggestion list has an opinion.

const suggestionsHook = vi.fn();

vi.mock('../../lib/api', () => ({
  useTicketRefSuggestions: (orgId: string, q: string) => suggestionsHook(orgId, q),
}));

const BEA42 = {
  ref: 'BEA-42', ticket_id: 't-42', number: 42, title: 'Reset the SSO metadata',
  space_id: 's-1', space_key: 'BEA', status: 'open', assigned_to_me: true,
};
const BEA7 = {
  ref: 'BEA-7', ticket_id: 't-7', number: 7, title: 'Rotate the signing key',
  space_id: 's-1', space_key: 'BEA', status: 'in_progress', assigned_to_me: false,
};

/** Prefix/substring match over the two fixtures, like the real endpoint. */
function matching(q: string) {
  const needle = q.trim().toLowerCase();
  if (!needle) return [];
  return [BEA42, BEA7].filter(
    (s) => s.ref.toLowerCase().startsWith(needle) || s.title.toLowerCase().includes(needle),
  );
}

const onChange = vi.fn();

function Harness({
  initial = '',
  ...rest
}: { initial?: string } & Omit<Partial<TicketRefFieldProps>, 'value' | 'onChange'>) {
  const [value, setValue] = useState(initial);
  return (
    <TicketRefField
      orgId="org-1"
      testId="tref"
      {...rest}
      value={value}
      onChange={(v) => {
        onChange(v);
        setValue(v);
      }}
    />
  );
}

const input = () => screen.getByTestId('tref') as HTMLInputElement;

beforeEach(() => {
  onChange.mockReset();
  suggestionsHook.mockReset();
  suggestionsHook.mockImplementation((orgId: string, q: string) => ({
    data: orgId ? matching(q) : [],
    isLoading: false,
    error: null,
  }));
});

describe('TicketRefField — free text is always valid', () => {
  // THE non-negotiable. A reference to a tracker Azimuthal has never heard of
  // must reach onChange character-for-character, stay in the input, and leave
  // the field valid — no rewrite, no error, no "pick a real ticket".
  it('passes an unmatched value through verbatim and never marks it invalid', () => {
    render(<Harness />);

    fireEvent.change(input(), { target: { value: 'SNOW-CHG0042' } });

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith('SNOW-CHG0042');
    expect(input().value).toBe('SNOW-CHG0042');

    // Nothing about an unmatched value is treated as an error.
    expect(input()).not.toHaveAttribute('aria-invalid', 'true');
    expect(input()).not.toBeRequired();
    expect(input().checkValidity()).toBe(true);
    expect(document.querySelector('[aria-invalid="true"]')).toBeNull();

    // The empty row says out loud that the typed text is what gets recorded.
    expect(screen.getByTestId('tref-empty').textContent).toContain('recorded exactly as typed');
  });

  // Regression guard against "fixing" this into a picker. Every dismissal
  // happens with matching suggestions ON SCREEN and one of them HIGHLIGHTED —
  // the exact state in which a picker would resolve the input to the
  // highlighted row. Dismissing must leave the typed text alone. (An earlier
  // draft dismissed only after typing past every match, and a mutation that
  // adopted the highlighted suggestion on Escape slipped through it.)
  it('keeps the typed text when the operator dismisses an offered suggestion', () => {
    render(<Harness />);

    fireEvent.change(input(), { target: { value: 'BEA' } });
    expect(screen.getByTestId('tref-option-BEA-42')).toBeInTheDocument();

    // Escape, with the first row highlighted by keyboard.
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    expect(screen.getByTestId('tref-option-BEA-42')).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(input(), { key: 'Escape' });
    expect(screen.queryByTestId('tref-suggestions')).toBeNull();
    expect(input().value).toBe('BEA');

    // Click-outside, with a row highlighted by hover.
    fireEvent.focus(input());
    fireEvent.mouseEnter(screen.getByTestId('tref-option-BEA-7'));
    expect(screen.getByTestId('tref-option-BEA-7')).toHaveAttribute('aria-selected', 'true');
    fireEvent.mouseDown(document.body);
    expect(screen.queryByTestId('tref-suggestions')).toBeNull();
    expect(input().value).toBe('BEA');

    // Tab away, again with a row highlighted.
    fireEvent.focus(input());
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'Tab' });
    expect(input().value).toBe('BEA');

    // Typing past every match clears the list without touching the value.
    fireEvent.change(input(), { target: { value: 'BEA-9000-external' } });
    expect(screen.queryByTestId('tref-option-BEA-42')).toBeNull();
    expect(screen.getByTestId('tref-empty')).toBeInTheDocument();
    expect(input().value).toBe('BEA-9000-external');

    // At no point did a suggestion's ref reach the caller.
    expect(onChange).toHaveBeenLastCalledWith('BEA-9000-external');
    expect(onChange).not.toHaveBeenCalledWith('BEA-42');
    expect(onChange).not.toHaveBeenCalledWith('BEA-7');
  });

  // Enter is the submit key of the dialogs this field lives in. It may only
  // accept a suggestion the operator explicitly highlighted; after plain
  // typing it must leave the value alone, or Enter would silently swap the
  // operator's reference for whatever happened to be first.
  it('Enter accepts only an explicitly highlighted suggestion', () => {
    render(<Harness />);

    fireEvent.change(input(), { target: { value: 'BEA' } });
    onChange.mockClear();

    // Nothing highlighted: Enter changes nothing.
    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onChange).not.toHaveBeenCalled();
    expect(input().value).toBe('BEA');

    // Highlight the second row, then Enter takes exactly that row's ref.
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'Enter' });

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith('BEA-7');
    expect(input().value).toBe('BEA-7');
  });
});

describe('TicketRefField — the suggestion list', () => {
  // The list has to be readable enough to choose between two tickets numbered
  // alike: the ref leads, the title follows, and the operator's own tickets
  // are marked.
  it('renders each suggestion as ref + title and marks the ones assigned to me', () => {
    render(<Harness />);

    fireEvent.change(input(), { target: { value: 'BEA' } });

    const list = screen.getByTestId('tref-suggestions');
    const assigned = within(list).getByTestId('tref-option-BEA-42');
    const other = within(list).getByTestId('tref-option-BEA-7');

    expect(assigned.textContent).toContain('BEA-42');
    expect(assigned.textContent).toContain('Reset the SSO metadata');
    expect(other.textContent).toContain('BEA-7');
    expect(other.textContent).toContain('Rotate the signing key');

    // The marker is on the assigned ticket and only on it.
    expect(within(assigned).getByTestId('tref-option-BEA-42-assigned')).toBeInTheDocument();
    expect(within(other).queryByTestId('tref-option-BEA-7-assigned')).toBeNull();
  });

  // Choosing writes the ref — not the title, not the ticket id — and closes
  // the list, leaving an ordinary input the operator can keep editing.
  it('choosing a suggestion fills the input with its ref and closes the list', () => {
    render(<Harness />);

    fireEvent.change(input(), { target: { value: 'BEA' } });
    fireEvent.click(screen.getByTestId('tref-option-BEA-42'));

    expect(onChange).toHaveBeenLastCalledWith('BEA-42');
    expect(input().value).toBe('BEA-42');
    expect(screen.queryByTestId('tref-suggestions')).toBeNull();

    // Still a free-text input afterwards — no chip, no clear button, and it
    // can be typed straight over.
    expect(input()).toBeInstanceOf(HTMLInputElement);
    fireEvent.change(input(), { target: { value: 'BEA-42 and OPS-9' } });
    expect(input().value).toBe('BEA-42 and OPS-9');
  });

  // A dialog that merely renders the field must not query the org's tickets;
  // the typeahead wakes on first use. The hook receives '' for orgId until
  // then, which is how it disables itself.
  it('does not query the typeahead until the field is used', () => {
    render(<Harness initial="PRE-1" />);

    expect(suggestionsHook).toHaveBeenCalled();
    expect(suggestionsHook.mock.calls.every((c) => c[0] === '')).toBe(true);
    expect(screen.queryByTestId('tref-suggestions')).toBeNull();

    fireEvent.focus(input());

    expect(suggestionsHook).toHaveBeenLastCalledWith('org-1', 'PRE-1');
  });
});
