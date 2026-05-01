import { Component, type ErrorInfo, type ReactNode } from 'react';

interface Props {
  children: ReactNode;
  fallback?: ReactNode;
}

interface State {
  hasError: boolean;
}

/** Catches render errors in child components and displays a fallback UI. */
export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError(): State {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error('ErrorBoundary caught:', error, info);
  }

  render(): ReactNode {
    if (this.state.hasError) {
      return (
        this.props.fallback ?? (
          <div className="flex flex-col items-center justify-center gap-4 py-16 text-center">
            <p className="text-[var(--text-lg)] text-[var(--color-text)]">
              Something went wrong loading this space.
            </p>
            <a
              href="/"
              className="text-[var(--text-sm)] text-[var(--color-primary)] hover:underline"
            >
              Go back to dashboard
            </a>
          </div>
        )
      );
    }

    return this.props.children;
  }
}
