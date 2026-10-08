// Package ports declares what the core needs from the outside world, in domain terms.
// Adapters implement these; the engine and nodes depend only on them.
package ports

// Page is one page of a cursor-paginated listing. NextCursor is empty on the last page.
// To fetch the next page, repeat the same filter with only the cursor changed.
type Page[T any] struct {
	Items      []T
	NextCursor string
}
