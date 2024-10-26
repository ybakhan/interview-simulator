package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var server = NewServer(Config{
		Address:     defaultAddress,
		GracePeriod: defaultGracePeriod,
	})

	go func() {
		if err := server.Start(ctx); err != nil {
			fmt.Println("Error starting server:", err)
		}
	}()

	<-ctx.Done()
	server.Stop()
}
