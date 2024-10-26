package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sync"

	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const minGracePeriod = time.Second

func TestSchemeSimulator(t *testing.T) {
	server, shutdown := startServer(Config{})
	defer stopServer(server, shutdown)

	tests := []struct {
		name           string
		input          string
		expectedOutput string
		minDuration    time.Duration
		maxDuration    time.Duration
	}{
		{
			name:           "Valid Request",
			input:          "PAYMENT|10",
			expectedOutput: "RESPONSE|ACCEPTED|Transaction processed",
			maxDuration:    50 * time.Millisecond,
		},
		{
			name:           "Valid Request with Delay",
			input:          "PAYMENT|101",
			expectedOutput: "RESPONSE|ACCEPTED|Transaction processed",
			minDuration:    101 * time.Millisecond,
			maxDuration:    151 * time.Millisecond,
		},

		{
			name:           "Invalid Request Format",
			input:          "INVALID|100",
			expectedOutput: "RESPONSE|REJECTED|Invalid request",
			maxDuration:    10 * time.Millisecond,
		},
		{
			name:           "Invalid amount",
			input:          "PAYMENT|ABC",
			expectedOutput: "RESPONSE|REJECTED|Invalid amount",
			maxDuration:    10 * time.Millisecond,
		},
		{
			name:           "Large Amount",
			input:          "PAYMENT|20000",
			expectedOutput: "RESPONSE|ACCEPTED|Transaction processed",
			minDuration:    10 * time.Second,
			maxDuration:    10*time.Second + 50*time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", ":8080")
			require.NoError(t, err, "Failed to connect to server")
			defer conn.Close()

			_, err = fmt.Fprintf(conn, tt.input+"\n")
			require.NoError(t, err, "Failed to send request")

			start := time.Now()

			response, err := bufio.NewReader(conn).ReadString('\n')
			require.NoError(t, err, "Failed to read response")
			duration := time.Since(start)

			response = strings.TrimSpace(response)

			assert.Equal(t, tt.expectedOutput, response, "Unexpected response")

			if tt.minDuration > 0 {
				assert.GreaterOrEqual(t, duration, tt.minDuration, "Response time was shorter than expected")
			}

			if tt.maxDuration > 0 {
				assert.LessOrEqual(t, duration, tt.maxDuration, "Response time was longer than expected")
			}
		})
	}
}

func TestMultipleConnectionsRequestsAccepted(t *testing.T) {
	server, shutdown := startServer(Config{GracePeriod: minGracePeriod})
	defer stopServer(server, shutdown)

	var wg sync.WaitGroup
	for i := 1; i <= 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn := initConnection(t)
			defer conn.Close()

			sendRequestAndAssertResponse(t, conn, "PAYMENT|100", responseAccepted, 0, 50*time.Millisecond)
		}()
	}

	wg.Wait()
}

func TestRequestAcceptedBeforeGracePeriod(t *testing.T) {
	server, shutdown := startServer(Config{GracePeriod: minGracePeriod})

	// open connection to enable timeout on shutdown
	conn := initConnection(t)
	defer conn.Close()

	// avoid race condition where wg.Add(1) doesn't register on connection
	// and wg is empty on shutdown resulting in immediate shutdown
	time.Sleep(50 * time.Millisecond)

	// trigger shutdown
	done := make(chan struct{})
	go func() {
		stopServer(server, shutdown)
		close(done)
	}()

	maxDuration := 50 * time.Millisecond
	sendRequestAndAssertResponse(t, conn, "PAYMENT|100", responseAccepted, 0, maxDuration)
	sendRequestAndAssertResponse(t, conn, "PAYMENT|200", responseAccepted, 0, 200*time.Millisecond+maxDuration)

	<-done
}

func TestMultipleConnectionsRequestsAcceptedBeforeGracePeriod(t *testing.T) {
	server, shutdown := startServer(Config{GracePeriod: minGracePeriod})

	// open connections to enable timeout on shutdown
	var connections []net.Conn
	for i := 1; i <= 10; i++ {
		connection := initConnection(t)
		connections = append(connections, connection)
		defer connection.Close()
	}

	// trigger shutdown
	done := make(chan struct{})
	go func() {
		stopServer(server, shutdown)
		close(done)
	}()

	var wg sync.WaitGroup
	for _, conn := range connections {
		wg.Add(1)
		go func() {
			defer wg.Done()

			maxDuration := 50 * time.Millisecond
			sendRequestAndAssertResponse(t, conn, "PAYMENT|100", responseAccepted, 0, maxDuration)
		}()
	}

	wg.Wait()
	<-done
}

func TestRequestCancelledAfterGracePeriod(t *testing.T) {
	gracePeriod := minGracePeriod
	server, shutdown := startServer(Config{GracePeriod: gracePeriod})

	// open connection to enable timeout on shutdown
	conn := initConnection(t)
	defer conn.Close()

	// avoid race condition where wg.Add(1) doesn't register on connection
	// and wg is empty on shutdown resulting in immediate shutdown and request accepted
	time.Sleep(50 * time.Millisecond)

	// trigger shutdown
	done := make(chan struct{})
	go func() {
		stopServer(server, shutdown)
		close(done)
	}()

	// request longer than timeout so it can be cancelled after timeout
	sendRequestAndAssertResponse(t, conn, "PAYMENT|1050", responseCancelled, 0, gracePeriod+50*time.Millisecond)

	<-done
}

func TestMultipleConnectionsRequestsCancelledAfterGracePeriod(t *testing.T) {
	gracePeriod := minGracePeriod
	server, shutdown := startServer(Config{GracePeriod: gracePeriod})

	// open connections to enable timeout on shutdown
	var connections []net.Conn
	for i := 1; i <= 10; i++ {
		connection := initConnection(t)
		connections = append(connections, connection)
		defer connection.Close()
	}

	// trigger shutdown
	done := make(chan struct{})
	go func() {
		stopServer(server, shutdown)
		close(done)
	}()

	var wg sync.WaitGroup
	for _, conn := range connections {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// request longer than timeout so they can be cancelled after timeout
			sendRequestAndAssertResponse(t, conn, "PAYMENT|1050", responseCancelled, 0, gracePeriod+50*time.Millisecond)
		}()
	}

	wg.Wait()
	<-done
}

func TestPendingRequestDiscardedAfterInProgressRequestCancelled(t *testing.T) {
	gracePeriod := time.Second
	server, shutdown := startServer(Config{GracePeriod: gracePeriod})

	// open connection to enable timeout on shutdown
	conn := initConnection(t)
	defer conn.Close()

	// avoid race condition where wg.Add(1) doesn't immediately register on connection
	// and wg is empty on shutdown resulting in immediate shutdown and request accepted
	time.Sleep(50 * time.Millisecond)

	// trigger shutdown
	done := make(chan struct{})
	go func() {
		stopServer(server, shutdown)
		close(done)
	}()

	// cancelled request
	sendRequest(t, conn, "PAYMENT|3000")

	// discarded request
	sendRequest(t, conn, "PAYMENT|100")

	// cancelled response received
	response, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err, "Failed to read response")
	require.Equal(t, responseCancelled, strings.TrimSpace(response), "Unexpected response")

	// discared request returns error
	response, err = bufio.NewReader(conn).ReadString('\n')
	assert.Error(t, err, "Failed to read response")

	<-done
}

func TestConnectionRefusedAfterShutdown(t *testing.T) {
	server, shutdown := startServer(Config{GracePeriod: minGracePeriod})

	// open connection to enable timeout on shutdown
	conn := initConnection(t)
	defer conn.Close()

	// trigger shutdown
	done := make(chan struct{})
	go func() {
		stopServer(server, shutdown)
		close(done)
	}()

	// make sure shutdown in progress
	time.Sleep(50 * time.Millisecond)

	_, err := net.Dial("tcp", ":8080")
	require.Error(t, err, "Failed to connect to server")

	<-done
}

func TestMultipleConnectionsRefusedAfterShutdown(t *testing.T) {
	server, shutdown := startServer(Config{GracePeriod: minGracePeriod})

	// open connection to enable timeout on shutdown
	conn := initConnection(t)
	defer conn.Close()

	// trigger shutdown
	done := make(chan struct{})
	go func() {
		stopServer(server, shutdown)
		close(done)
	}()

	// make sure shutdown in progress
	time.Sleep(50 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := net.Dial("tcp", ":8080")
			require.Error(t, err, "Failed to connect to server")
		}()
	}

	wg.Wait()
	<-done
}

func sendRequestAndAssertResponse(t *testing.T, conn net.Conn, request, expectedResponse string, minDuration, maxDuration time.Duration) {
	start := time.Now()

	sendRequest(t, conn, request)

	response, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err, "Failed to read response")

	assert.Equal(t, expectedResponse, strings.TrimSpace(response), "Unexpected response")

	duration := time.Since(start)
	if minDuration > 0 {
		assert.GreaterOrEqual(t, duration, minDuration, "Response time was shorter than expected")
	}
	if maxDuration > 0 {
		assert.LessOrEqual(t, duration, maxDuration, "Response time was longer than expected")
	}
}

func initConnection(t *testing.T) net.Conn {
	conn, err := net.Dial("tcp", ":8080")
	require.NoError(t, err, "Failed to connect to server")
	return conn
}

func sendRequest(t *testing.T, conn net.Conn, request string) {
	_, err := fmt.Fprintf(conn, request+"\n")
	require.NoError(t, err, "Failed to send request")
}

func startServer(config Config) (Server, context.CancelFunc) {
	server := NewServer(config)
	shutdownCtx, shutdown := context.WithCancel(context.Background())
	server.Start(shutdownCtx)
	return server, shutdown
}

func stopServer(server Server, shutdown context.CancelFunc) {
	shutdown()
	server.Stop()
}
