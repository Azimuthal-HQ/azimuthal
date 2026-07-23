import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { SegmentedControl } from '../segmented';
import { PRIORITY_SEGMENT_OPTIONS, type PriorityKey } from '../../priority';

const PLAIN_OPTIONS = [
  { value: 'user', label: 'User' },
  { value: 'team', label: 'Team' },
] as const;

function PlainHarness({ initial = 'user' }: { initial?: 'user' | 'team' }) {
  const [value, setValue] = useState<'user' | 'team'>(initial);
  return (
    <SegmentedControl
      options={[...PLAIN_OPTIONS]}
      value={value}
      onChange={setValue}
      aria-label="Subject type"
    />
  );
}

function PriorityHarness() {
  const [value, setValue] = useState<PriorityKey>('medium');
  return (
    <SegmentedControl
      options={PRIORITY_SEGMENT_OPTIONS}
      value={value}
      onChange={setValue}
      aria-label="Priority"
    />
  );
}

const radio = (name: string) => screen.getByRole('radio', { name });

describe('SegmentedControl', () => {
  it('renders a radiogroup with one radio per option and the value checked', () => {
    render(<PlainHarness />);
    expect(screen.getByRole('radiogroup', { name: 'Subject type' })).toBeInTheDocument();
    expect(screen.getAllByRole('radio')).toHaveLength(2);
    expect(radio('User')).toHaveAttribute('aria-checked', 'true');
    expect(radio('Team')).toHaveAttribute('aria-checked', 'false');
  });

  it('selects on click', () => {
    render(<PlainHarness />);
    fireEvent.click(radio('Team'));
    expect(radio('Team')).toHaveAttribute('aria-checked', 'true');
    expect(radio('User')).toHaveAttribute('aria-checked', 'false');
  });

  it('moves selection with arrow keys, wrapping at the ends, and supports Home/End', () => {
    render(<PriorityHarness />);

    radio('Medium').focus();
    fireEvent.keyDown(radio('Medium'), { key: 'ArrowRight' });
    expect(radio('High')).toHaveAttribute('aria-checked', 'true');
    expect(radio('High')).toHaveFocus();

    fireEvent.keyDown(radio('High'), { key: 'ArrowRight' });
    fireEvent.keyDown(radio('Critical'), { key: 'ArrowRight' });
    // Critical → wraps to Low.
    expect(radio('Low')).toHaveAttribute('aria-checked', 'true');

    fireEvent.keyDown(radio('Low'), { key: 'ArrowLeft' });
    expect(radio('Critical')).toHaveAttribute('aria-checked', 'true');

    fireEvent.keyDown(radio('Critical'), { key: 'Home' });
    expect(radio('Low')).toHaveAttribute('aria-checked', 'true');
    fireEvent.keyDown(radio('Low'), { key: 'End' });
    expect(radio('Critical')).toHaveAttribute('aria-checked', 'true');
  });

  it('keeps exactly the selected option tabbable (roving tabindex)', () => {
    render(<PriorityHarness />);
    const tabbable = screen
      .getAllByRole('radio')
      .filter((r) => r.getAttribute('tabindex') === '0');
    expect(tabbable).toHaveLength(1);
    expect(tabbable[0]).toHaveAccessibleName('Medium');
  });

  it('renders a colour dot per option in the priority variant', () => {
    const { container } = render(<PriorityHarness />);
    // One dot per level, hidden from the accessibility tree.
    const dots = container.querySelectorAll('span[aria-hidden="true"]');
    expect(dots).toHaveLength(PRIORITY_SEGMENT_OPTIONS.length);
  });
});
