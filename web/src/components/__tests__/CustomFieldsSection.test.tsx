import { render, screen, fireEvent } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CustomFieldsSection } from '../CustomFieldsSection';

// MockAPIError stands in for the api module's APIError: the component reads
// `err instanceof APIError` to decide the failure carried a server message, so
// the class the test rejects with must be the same one the mock exports. It is
// built through vi.hoisted because the vi.mock factory below reads it eagerly
// (`APIError: MockAPIError`), and that factory is hoisted above a plain class
// declaration — which would still be in its temporal dead zone at that point.
const { MockAPIError } = vi.hoisted(() => {
  class MockAPIError extends Error {
    code: string;
    status: number;
    constructor(status: number, code: string, message: string) {
      super(message);
      this.name = 'APIError';
      this.code = code;
      this.status = status;
    }
  }
  return { MockAPIError };
});

// saveError is what the next field save rejects with (null = it succeeds). The
// mocked mutate invokes onError with it synchronously, which is what react-query
// does on a failed mutation.
let saveError: unknown = null;
const setMutate = vi.fn(
  (_vars: { slug: string; value: string }, opts?: { onError?: (e: unknown) => void }) => {
    if (saveError) opts?.onError?.(saveError);
  },
);

// invalidateSpy stands in for queryClient.invalidateQueries — the save-error
// path must refetch the entity's fields so a definition that moved out from
// under the open form (archived, detached, deleted) re-renders as read-only.
const invalidateSpy = vi.fn();
vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ invalidateQueries: invalidateSpy }),
}));

// serverFields is what the query currently holds. Reassigning it and
// re-rendering is a refetch: the component reads a new array with a new value,
// which is exactly what an invalidate-then-refetch delivers in production.
//
// The value MUST differ between the two renders for any of this to be a test.
// The seeding effect depends on `field.value`, a string, so a refetch returning
// the same text never re-runs it — a test that re-rendered with identical data
// would pass with the guard deleted and assert nothing (CLAUDE.md section 2).
// options is widened to string[] so single-select cases below (which carry
// real option lists) stay assignable to serverFields' inferred element type.
const initialFields = () => [
  { slug: 'points', name: 'Points', field_type: 'number', options: [] as string[], value: '5', required: false, legacy: false },
  { slug: 'squad', name: 'Squad', field_type: 'text', options: [] as string[], value: 'Falcon', required: false, legacy: true },
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
  APIError: MockAPIError,
  queryKeys: {
    entityFields: (spaceId: string, kind: string, entityId: string) =>
      ['entityFields', spaceId, kind, entityId],
  },
}));

afterEach(() => {
  // mockClear, not mockReset: the mutate stub keeps its onError-invoking
  // implementation across tests; only the recorded calls are cleared.
  setMutate.mockClear();
  invalidateSpy.mockClear();
  saveError = null;
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

  // THE C3 DEFECT, both halves. A definition archived while this page is open
  // leaves a stale form: the control is still editable with its required
  // asterisk, and the save is refused with a bare "Could not save." The fix
  // surfaces the server's honest message AND refetches so the row re-renders as
  // read-only legacy — the asterisk gone — within one round trip.
  const ARCHIVED_MESSAGE =
    'this custom field was archived; its stored value is read-only and cannot be changed';

  it('surfaces the server refusal and refetches so an archived field re-renders read-only', () => {
    // A required, editable single-select — the exact control the maintainer
    // watched stay editable after archiving the definition mid-edit.
    serverFields = [
      { slug: 'stage', name: 'Stage', field_type: 'single_select', options: ['alpha', 'beta'], value: 'alpha', required: true, legacy: false },
    ];
    saveError = new MockAPIError(404, 'NOT_FOUND', ARCHIVED_MESSAGE);

    const { rerender } = render(<CustomFieldsSection {...sectionProps} />);

    // Before: an editable select carrying the required marker.
    const select = screen.getByLabelText(/Stage/) as HTMLSelectElement;
    expect(select.tagName).toBe('SELECT');
    expect(screen.getByTitle('Required in this space')).toBeInTheDocument();

    // Changing it saves, and the server refuses with the archived message.
    fireEvent.change(select, { target: { value: 'beta' } });

    // Half 1 — the server's own message renders, not the generic fallback.
    // Fails-before: the old code ran it through friendlyErrorMessage, which the
    // mock collapses to the fallback, so "Could not save." showed instead.
    expect(screen.getByText(ARCHIVED_MESSAGE)).toBeInTheDocument();
    expect(screen.queryByText('Could not save.')).toBeNull();

    // Half 2 — the failed save invalidated this entity's field query.
    // Fails-before: the old code invalidated on success only, never on error.
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ['entityFields', 's1', 'item', 'i1'],
    });

    // The refetch that invalidation triggers brings the field back as archived
    // legacy; the row must flip to the read-only control with the asterisk gone.
    serverFields = [
      { slug: 'stage', name: 'Stage', field_type: 'single_select', options: ['alpha', 'beta'], value: 'alpha', required: true, legacy: true },
    ];
    rerender(<CustomFieldsSection {...sectionProps} />);

    expect(screen.queryByLabelText(/Stage/)).toBeNull(); // no editable control
    expect(screen.queryByTitle('Required in this space')).toBeNull(); // asterisk gone
    expect(screen.getByText('legacy')).toBeInTheDocument();
    expect(screen.getByText('alpha')).toBeInTheDocument(); // stored value shown read-only
  });

  // The general fix, not the archived one alone: any structured save failure
  // shows the server's message and refetches. A transport/parse failure that
  // carries no envelope falls back to the generic line.
  it('falls back to the generic line when the failure carries no server message', () => {
    serverFields = [
      { slug: 'points', name: 'Points', field_type: 'number', options: [], value: '5', required: false, legacy: false },
    ];
    saveError = new Error('network down'); // not an APIError — no envelope

    render(<CustomFieldsSection {...sectionProps} />);
    const input = screen.getByLabelText('Points');
    fireEvent.change(input, { target: { value: '8' } });
    fireEvent.blur(input);

    expect(screen.getByText('Could not save.')).toBeInTheDocument();
    // Even a transport failure refetches — the form may be stale for a reason
    // the error body could not name.
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ['entityFields', 's1', 'item', 'i1'],
    });
  });
});
