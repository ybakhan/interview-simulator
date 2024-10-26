package main

import (
	"context"
	"strconv"
	"strings"
	"time"
)

const (
	responseCancelled = "RESPONSE|REJECTED|Cancelled"
	responseAccepted  = "RESPONSE|ACCEPTED|Transaction processed"
)

func handleRequest(ctx context.Context, request string) string {
	parts := strings.Split(request, "|")
	if len(parts) != 2 || parts[0] != "PAYMENT" {
		return "RESPONSE|REJECTED|Invalid request"
	}

	amount, err := strconv.Atoi(parts[1])
	if err != nil {
		return "RESPONSE|REJECTED|Invalid amount"
	}

	if amount <= 100 {
		return responseAccepted
	}

	processingTime := amount
	if amount > 10000 {
		processingTime = 10000
	}

	processingDuration := time.Duration(processingTime) * time.Millisecond

	select {
	case <-time.After(processingDuration):
		return responseAccepted
	case <-ctx.Done():
		return responseCancelled
	}
}
