import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { RadioCardGroup, type RadioCardOption } from '../radio-card';

type Vis = 'hidden' | 'discoverable' | 'org';

const OPTIONS: RadioCardOption<Vis>[] = [
  { value: 'hidden', title: 'Hidden', description: 'Invisible except to people with a grant.' },
  { value: 'discoverable', title: 'Discoverable', description: 'Listed for everyone; locked without a grant.' },
  { value: 'org', title: 'Org', description: 'Everyone in the org can view as a viewer.' },
];

function Harness({ initial = 'discoverable' as Vis }) {
  const [value, setValue] = useState<Vis>(initial);
  return (
    <RadioCardGroup
      options={OPTIONS}
      value={value}
      onChange={setValue}
      aria-label="Visibility"
    />
  );
}

const radio = (name: RegExp) => screen.getByRole('radio', { name });

describe('RadioCardGroup', () => {
  it('renders one radio card per option with title and description', () => {
    render(<Harness />);
    expect(screen.getByRole('radiogroup', { name: 'Visibility' })).toBeInTheDocument();
    expect(screen.getAllByRole('radio')).toHaveLength(3);
    expect(screen.getByText('Hidden')).toBeInTheDocument();
    expect(screen.getByText('Listed for everyone; locked without a grant.')).toBeInTheDocument();
    expect(radio(/Discoverable/)).toHaveAttribute('aria-checked', 'true');
  });

  it('selects on click', () => {
    render(<Harness />);
    fireEvent.click(radio(/Org/));
    expect(radio(/Org/)).toHaveAttribute('aria-checked', 'true');
    expect(radio(/Discoverable/)).toHaveAttribute('aria-checked', 'false');
  });

  it('moves selection with arrow keys, wrapping past the ends', () => {
    render(<Harness />);

    radio(/Discoverable/).focus();
    fireEvent.keyDown(radio(/Discoverable/), { key: 'ArrowDown' });
    expect(radio(/Org/)).toHaveAttribute('aria-checked', 'true');
    expect(radio(/Org/)).toHaveFocus();

    // Wraps past the end.
    fireEvent.keyDown(radio(/Org/), { key: 'ArrowDown' });
    expect(radio(/Hidden/)).toHaveAttribute('aria-checked', 'true');

    fireEvent.keyDown(radio(/Hidden/), { key: 'ArrowUp' });
    expect(radio(/Org/)).toHaveAttribute('aria-checked', 'true');
  });

  it('confirms the focused option with Space and Enter', () => {
    render(<Harness />);
    radio(/Hidden/).focus();
    fireEvent.keyDown(radio(/Hidden/), { key: 'Enter' });
    expect(radio(/Hidden/)).toHaveAttribute('aria-checked', 'true');
    fireEvent.keyDown(radio(/Hidden/), { key: ' ' });
    expect(radio(/Hidden/)).toHaveAttribute('aria-checked', 'true');
  });

  it('keeps exactly one card tabbable', () => {
    render(<Harness />);
    const tabbable = screen
      .getAllByRole('radio')
      .filter((r) => r.getAttribute('tabindex') === '0');
    expect(tabbable).toHaveLength(1);
  });
});
