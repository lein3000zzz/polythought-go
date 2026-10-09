import { Link } from 'react-router'

export function NotFoundPage() {
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-2 p-6 text-center">
      <h1 className="font-heading text-xl font-semibold">Page not found</h1>
      <Link to="/" className="text-sm underline underline-offset-4">
        Go to your workflows
      </Link>
    </div>
  )
}
