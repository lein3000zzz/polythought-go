/**
 * The page to go to after signing in: the `next` parameter when it is a path on this
 * site, so a crafted link cannot send people elsewhere.
 */
export function nextPath(next: string | null): string {
  if (next && next.startsWith('/') && !next.startsWith('//') && !next.startsWith('/\\')) {
    return next
  }

  return '/workflows'
}
