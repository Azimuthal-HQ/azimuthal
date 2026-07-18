import { Link } from 'react-router-dom';
import { Compass } from 'lucide-react';
import { Button } from '../components/ui/button';
import { EmptyState } from './EmptyState';

/**
 * Catch-all page for URLs that match no route.
 *
 * This is the structural guard behind the P0 blank-screen defects: the
 * sidebar linked to routes that were never registered, and React Router
 * rendered an empty body. Any unmatched path now lands here instead.
 */
export function NotFoundPage() {
  return (
    <EmptyState
      icon={Compass}
      title="Page not found"
      description="This page doesn't exist — it may have moved, or the link is out of date."
      action={
        <Button asChild>
          <Link to="/">Back to Home</Link>
        </Button>
      }
    />
  );
}
