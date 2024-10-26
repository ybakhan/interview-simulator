package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

const (
	defaultGracePeriod = 3 * time.Second
	defaultAddress     = "localhost:8080"
)

func NewServer(config Config) Server {
	if config.Address == "" {
		config.Address = defaultAddress
	}

	if config.GracePeriod <= 0 {
		config.GracePeriod = defaultGracePeriod
	}
	return &server{config: config}
}

func (s *server) Start(shutdownCtx context.Context) error {
	listener, err := net.Listen("tcp", s.config.Address)
	if err != nil {
		return err
	}

	fmt.Println("Server started on", s.config.Address)

	go func() {
		<-shutdownCtx.Done()
		if err := listener.Close(); err != nil {
			fmt.Println("Error closing listener:", err)
		}
	}()

	go func() {
		for {
			// check if shutdown is triggered before accepting connections
			if shutdownCtx.Err() != nil {
				return
			}

			conn, err := listener.Accept()
			if err != nil {
				if shutdownCtx.Err() != nil {
					return // shutdown in progress
				}

				fmt.Println("Error accepting connection:", err)
				continue // ignore error and try accepting again
			}

			s.wg.Add(1)
			go s.handleConnection(conn)
		}
	}()
	return nil
}

func (s *server) Stop() {
	fmt.Println("Shutting down...")
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	// wait for either WaitGroup to finish or timeout to occur
	select {
	case <-done:
		// shutdown complete
	case <-time.After(s.config.GracePeriod):
		fmt.Println("Grace period exceeded. Cancelling requests in progress.")
		// cancel any remaining requests
		s.connections.Range(func(key, value interface{}) bool {
			if cancel, ok := value.(context.CancelFunc); ok {
				cancel()
				fmt.Printf("Request %d cancelled due to shutdown.\n", key.(int64))
			}
			return true
		})
	}

	fmt.Println("Shutdown complete")
}

func (s *server) handleConnection(conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			fmt.Println("Error closing connection:", err)
		}
		s.wg.Done()
	}()

	// create a cancellable context for this connection
	connectionID := s.generateConnectionID()
	connectionCtx, cancelConnection := context.WithCancel(context.Background())
	defer cancelConnection()

	s.connections.Store(connectionID, cancelConnection)
	defer s.connections.Delete(connectionID)

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		if connectionCtx.Err() != nil {
			return
		}

		request := scanner.Text()
		response := handleRequest(connectionCtx, request)
		fmt.Fprintf(conn, "%s\n", response)
	}

	if err := scanner.Err(); err != nil && connectionCtx.Err() == nil {
		fmt.Println("Error reading from connection:", err)
	}
}

// generateConnectionID generates a unique ID for each request using atomic operations.
func (s *server) generateConnectionID() int64 {
	return atomic.AddInt64(&s.connectionID, 1)
}
