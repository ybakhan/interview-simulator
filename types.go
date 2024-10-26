package main

import (
	"context"
	"sync"
	"time"
)

type (
	Server interface {
		Start(ctx context.Context) error
		Stop()
	}

	Config struct {
		Address     string
		GracePeriod time.Duration
	}

	server struct {
		config       Config
		wg           sync.WaitGroup
		connections  sync.Map
		connectionID int64 // Atomic counter for request IDs
	}
)
