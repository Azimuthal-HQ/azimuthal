import { render, screen, fireEvent } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CustomFieldsSection } from '../CustomFieldsSection';

const setMutate = vi.fn();

// serverFields is what the query currently holds. Reassigning it and
// re-rendering is a refetch: the component reads a new array with a new value,
// which is exactly what an invalidate-then-refetch delivers in production.
//
// The value MUST differ between the two renders for any of this to be a test.
// The seeding effect depends on `field.value`, a string, so a refetch returning
// the same text never re-runs it — a test that re-rendered with identical data
// would pass with the guard deleted and assert nothing (CLAUDE.md section 2).
const initialFields = () => [
  { slug: 'points', name: 'Points', field_type: 'number', options: [], value: '5', required: false, legacy: false },
  { slug: 'squad', name: 'Squad', field_type: 'text', options: [], value: 'Falcon', required: false, legacy: true },
];
let serverFields = initialFields();

function withServerValue(slug: string, value: string) {
  serverFields = serverFields.map((f) => (f.slug === slug ? { ...f, value } : f));
}

// The full query result shape, isError included. The previous mock had no
// isError key at all, which is exactly why the component's missing error
// branch went unnoticed: a failed fetch rendered as "this entity has no
// custom fields" and nothing could ever catch it here.
const useEntityFieldsMock = vi.fn(() => ({
  data: serverFields as unknown,
  isLoading: false,
  isError: false,
  error: null as unknown,
}));

vi.mock('../../lib/api', () => ({
  useEntityFields: () => useEntityFieldsMock(),
  useSetEntityField: () => ({ mutate: setMutate }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

afterEach(() => {
  setMutate.mockReset();
  useEntityFieldsMock.mockReset();
  serverFields = initialFields();
  useEntityFieldsMock.mockImplementation(() => ({
    data: serverFields as unknown,
    isLoading: false,
    isError: false,
    error: null as unknown,
  }));
});

const sectionProps = { spaceId: 's1', entityKind: 'item' as const, entityId: 'i1' };

describe('CustomFieldsSection', () => {
  it('renders active fields editable and legacy fields read-only', () => {
    render(<CustomFieldsSection {...sectionProps} />);

    // Active number field is an editable input with its value.
    const input = screen.getByLabelText('Points') as HTMLInputElement;
    expect(input.value).toBe('5');
    expect(input.tagName).toBe('INPUT');

    // Legacy field is flagged and rendered as read-only text, not an input.
    expect(screen.getByText('legacy')).toBeInTheDocument();
    expect(screen.queryByLabelText('Squad')).toBeNull();
    expect(screen.getByText('Falcon')).toBeInTheDocument();
  });

  // THE DEFECT this section shipped with: isError was never read, so a failed
  // fetch rendered null — indistinguishable from the true empty case, on a
  // surface that carries required fields. A section that cannot load must say
  // it could not load.
  it('says the section could not load instead of rendering as empty', () => {
    useEntityFieldsMock.mockImplementation(() => ({
      data: undefined as unknown,
      isLoading: false,
      isError: true,
      error: new Error('boom') as unknown,
    }));
    render(<CustomFieldsSection {...sectionProps} />);

    expect(screen.getByTestId('custom-fields-error')).toBeInTheDocument();
    expect(screen.getByText('Custom fields could not be loaded.')).toBeInTheDocument();
  });

  // A required attachment is visible before the server has to refuse anything:
  // the label carries the marker and the control is aria-required.
  it('marks required fields on the form control', () => {
    serverFields = [
      { slug: 'severity', name: 'Severity', field_type: 'text', options: [], value: '', required: true, legacy: false },
    ];
    render(<CustomFieldsSection {...sectionProps} />);

    expect(screen.getByTitle('Required in this space')).toBeInTheDocument();
    const input = screen.getByLabelText(/Severity/) as HTMLInputElement;
    expect(input.getAttribute('aria-required')).toBe('true');
  });

  it('persists a changed value on blur', () => {
    render(<CustomFieldsSection {...sectionProps} />);
    const input = screen.getByLabelText('Points');
    fireEvent.change(input, { target: { value: '8' } });
    fireEvent.blur(input);
    expect(setMutate).toHaveBeenCalledTimes(1);
    expect(setMutate.mock.calls[0][0]).toEqual({ slug: 'points', value: '8' });
  });

  it('does not persist when the value is unchanged', () => {
    render(<CustomFieldsSection {...sectionProps} />);
    const input = screen.getByLabelText('Points');
    fireEvent.blur(input);
    expect(setMutate).not.toHaveBeenCalled();
  });

  // THE DEFECT (known-issues #17, still-open item 1). Every successful field
  // save invalidates the entity's whole field list, so a refetch routinely
  // lands while somebody is still typing in another field — or in the same
  // one, after a blur. Unguarded, the seeding effect overwrote what they had
  // typed.
  it('keeps an in-progress edit when a refetch changes the server value', () => {
    const { rerender } = render(<CustomFieldsSection {...sectionProps} />);
    const input = screen.getByLabelText('Points') as HTMLInputElement;

    fireEvent.change(input, { target: { value: '8' } }); // typing, not yet blurred

    withServerValue('points', '9'); // somebody else's save lands
    rerender(<CustomFieldsSection {...sectionProps} />);

    expect((screen.getByLabelText('Points') as HTMLInputElement).value).toBe('8');
  });

  // The other half, and the reason the flag exists rather than the effect just
  // being deleted: a row nobody is editing must still follow the server.
  it('picks up a server change when there is no edit in progress', () => {
    const { rerender } = render(<CustomFieldsSection {...sectionProps} />);
    expect((screen.getByLabelText('Points') as HTMLInputElement).value).toBe('5');

    withServerValue('points', '9');
    rerender(<CustomFieldsSection {...sectionProps} />);

    expect((screen.getByLabelText('Points') as HTMLInputElement).value).toBe('9');
  });

  // The flag must not be sticky. Once the server holds what is on screen the
  // edit is over, so the row starts following refetches again — otherwise one
  // keystroke would freeze a field against every later update for as long as
  // the page stayed open.
  it('follows the server again once a save has landed', () => {
    const { rerender } = render(<CustomFieldsSection {...sectionProps} />);
    const input = screen.getByLabelText('Points');

    fireEvent.change(input, { target: { value: '8' } });
    fireEvent.blur(input);
    expect(setMutate).toHaveBeenCalledTimes(1);

    // The save lands and the refetch brings the value back.
    withServerValue('points', '8');
    rerender(<CustomFieldsSection {...sectionProps} />);
    expect((screen.getByLabelText('Points') as HTMLInputElement).value).toBe('8');

    // A later change from anywhere is picked up again.
    withServerValue('points', '13');
    rerender(<CustomFieldsSection {...sectionProps} />);
    expect((screen.getByLabelText('Points') as HTMLInputElement).value).toBe('13');
  });

  // Typing a change and then typing it back is not an edit in progress either.
  it('follows the server again after an edit is undone by hand', () => {
    const { rerender } = render(<CustomFieldsSection {...sectionProps} />);
    const input = screen.getByLabelText('Points');

    fireEvent.change(input, { target: { value: '8' } });
    fireEvent.change(input, { target: { value: '5' } }); // back to the stored value

    withServerValue('points', '9');
    rerender(<CustomFieldsSection {...sectionProps} />);

    expect((screen.getByLabelText('Points') as HTMLInputElement).value).toBe('9');
  });
});
