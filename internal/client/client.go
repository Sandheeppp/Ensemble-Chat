package client

import (
	"distributed-chat-coordinator/internal/types"
	"fmt"
)

// BookingClient represents a client that interacts with the ticket booking system.
type BookingClient struct {
	ID     string
	prefix string
}

// NewBookingClient creates a new booking client.
func NewBookingClient(id string) *BookingClient {
	nodeNum := 0
	if len(id) > 0 {
		nodeNum = int(id[len(id)-1]) % 6
	}
	return &BookingClient{
		ID:     id,
		prefix: types.LogPrefix(nodeNum, "CLIENT-"+id),
	}
}

// SendBooking logs a booking request.
func (c *BookingClient) SendBooking(seatNum int) {
	fmt.Printf("%s📤 Requesting to book seat %d\n", c.prefix, seatNum)
}

// SendCancel logs a cancellation request.
func (c *BookingClient) SendCancel(seatNum int) {
	fmt.Printf("%s📤 Requesting to cancel seat %d\n", c.prefix, seatNum)
}

// RequestView logs a view request.
func (c *BookingClient) RequestView() {
	fmt.Printf("%s📤 Requesting seat status view\n", c.prefix)
}
