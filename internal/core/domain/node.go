package domain

import "context"

type Data map[string]any

type Node interface {
	ID() string
	Run(ctx context.Context, input Data) (Data, error)
}
