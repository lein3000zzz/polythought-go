package ports

import "context"

// RepoStars asks GitHub how many stars Polythought's repository has (D372):
// domain.ErrNotFound while GitHub shows no such repository, as while it is private, and
// domain.ErrUnavailable when GitHub cannot be asked.
type RepoStars interface {
	Stars(ctx context.Context) (int, error)
}
