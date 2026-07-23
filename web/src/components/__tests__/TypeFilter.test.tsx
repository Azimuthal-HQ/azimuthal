import { render, screen, fireEvent } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { TypeFilter } from '../TypeFilter';

const options = [
  { slug: 'bug', name: 'Bug' },
  { slug: 'task', name: 'Task' },
];

describe('TypeFilter', () => {
  it('renders one chip per type and reflects selection via aria-pressed', () => {
    render(<TypeFilter options={options} selected={new Set(['bug'])} onToggle={vi.fn()} />);
    const bug = screen.getByRole('button', { name: 'Bug' });
    const task = screen.getByRole('button', { name: 'Task' });
    expect(bug).toHaveAttribute('aria-pressed', 'true');
    expect(task).toHaveAttribute('aria-pressed', 'false');
  });

  it('calls onToggle with the type slug when a chip is clicked', () => {
    const onToggle = vi.fn();
    render(<TypeFilter options={options} selected={new Set()} onToggle={onToggle} />);
    fireEvent.click(screen.getByRole('button', { name: 'Task' }));
    expect(onToggle).toHaveBeenCalledWith('task');
  });

  it('renders nothing when there are no type options', () => {
    const { container } = render(<TypeFilter options={[]} selected={new Set()} onToggle={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });
});
