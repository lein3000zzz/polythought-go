// Package pgtest starts throwaway PostgreSQL containers for tests and tooling.
package pgtest

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Image matches deployments/docker-compose.yml.
const Image = "postgres:17-alpine"

// Container is a running database; Terminate removes it.
type Container struct {
	DSN       string
	Terminate func(context.Context) error
}

func Start(ctx context.Context) (*Container, error) {
	c, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("polythought"),
		tcpostgres.WithUsername("polythought"),
		tcpostgres.WithPassword("polythought"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("pgtest: start postgres: %w", err)
	}

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, fmt.Errorf("pgtest: connection string: %w", err)
	}

	return &Container{
		DSN:       dsn,
		Terminate: func(context.Context) error { return testcontainers.TerminateContainer(c) },
	}, nil
}
