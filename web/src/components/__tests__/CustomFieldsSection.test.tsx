import { render, screen, fireEvent } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CustomFieldsSection } from '../CustomFieldsSection';

const setMutate = vi.fn();

const useItemFieldsMock = vi.fn(() => ({
  data: [
    { slug: 'points', name: 'Points', field_type: 'number', options: [], value: '5', legacy: false },
    { slug: 'squad', name: 'Squad', field_type: 'text', options: [], value: 'Falcon', legacy: true },
  ],
  isLoading: false,
}));

vi.mock('../../lib/api', () => ({
  useItemFields: () => useItemFieldsMock(),
  useSetItemField: () => ({ mutate: setMutate }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

afterEach(() => setMutate.mockReset());

describe('CustomFieldsSection', () => {
  it('renders active fields editable and legacy fields read-only', () => {
    render(<CustomFieldsSection spaceId="s1" itemId="i1" />);

    // Active number field is an editable input with its value.
    const input = screen.getByLabelText('Points') as HTMLInputElement;
    expect(input.value).toBe('5');
    expect(input.tagName).toBe('INPUT');

    // Legacy field is flagged and rendered as read-only text, not an input.
    expect(screen.getByText('legacy')).toBeInTheDocument();
    expect(screen.queryByLabelText('Squad')).toBeNull();
    expect(screen.getByText('Falcon')).toBeInTheDocument();
  });

  it('persists a changed value on blur', () => {
    render(<CustomFieldsSection spaceId="s1" itemId="i1" />);
    const input = screen.getByLabelText('Points');
    fireEvent.change(input, { target: { value: '8' } });
    fireEvent.blur(input);
    expect(setMutate).toHaveBeenCalledTimes(1);
    expect(setMutate.mock.calls[0][0]).toEqual({ slug: 'points', value: '8' });
  });

  it('does not persist when the value is unchanged', () => {
    render(<CustomFieldsSection spaceId="s1" itemId="i1" />);
    const input = screen.getByLabelText('Points');
    fireEvent.blur(input);
    expect(setMutate).not.toHaveBeenCalled();
  });
});
