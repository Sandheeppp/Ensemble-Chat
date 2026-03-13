package booking

import (
	"fmt"
	"sync"

	"distributed-chat-coordinator/internal/types"
)

// SeatDB manages the local seat database for ticket booking.
// Each node maintains its own copy; synchronization happens via peer RPCs.
type SeatDB struct {
	mu     sync.Mutex
	seats  map[int]string // seat number -> "available" or "booked"
	prefix string
}

// NewSeatDB creates a new seat database with all seats initialized as "available".
func NewSeatDB(nodeID int) *SeatDB {
	seats := make(map[int]string)
	for i := 1; i <= types.TotalSeats; i++ {
		seats[i] = "available"
	}
	return &SeatDB{
		seats:  seats,
		prefix: types.LogPrefix(nodeID, "BOOKING"),
	}
}

// BookSeat attempts to book the given seat number.
// Returns (success, message).
func (db *SeatDB) BookSeat(seatNum int) (bool, string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if seatNum < 1 || seatNum > types.TotalSeats {
		msg := fmt.Sprintf("Invalid seat number %d (valid: 1-%d)", seatNum, types.TotalSeats)
		fmt.Printf("%s[FAIL] %s\n", db.prefix, msg)
		return false, msg
	}

	status, exists := db.seats[seatNum]
	if !exists {
		msg := fmt.Sprintf("Seat %d does not exist", seatNum)
		fmt.Printf("%s[FAIL] %s\n", db.prefix, msg)
		return false, msg
	}

	if status == "booked" {
		msg := fmt.Sprintf("Seat %d is already booked", seatNum)
		fmt.Printf("%s[FAIL] %s\n", db.prefix, msg)
		return false, msg
	}

	db.seats[seatNum] = "booked"
	msg := fmt.Sprintf("Seat %d booked successfully", seatNum)
	fmt.Printf("%s[OK] %s\n", db.prefix, msg)
	return true, msg
}

// CancelSeat attempts to cancel a booking for the given seat number.
// Returns (success, message).
func (db *SeatDB) CancelSeat(seatNum int) (bool, string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if seatNum < 1 || seatNum > types.TotalSeats {
		msg := fmt.Sprintf("Invalid seat number %d (valid: 1-%d)", seatNum, types.TotalSeats)
		fmt.Printf("%s[FAIL] %s\n", db.prefix, msg)
		return false, msg
	}

	status, exists := db.seats[seatNum]
	if !exists {
		msg := fmt.Sprintf("Seat %d does not exist", seatNum)
		fmt.Printf("%s[FAIL] %s\n", db.prefix, msg)
		return false, msg
	}

	if status == "available" {
		msg := fmt.Sprintf("Seat %d is not booked", seatNum)
		fmt.Printf("%s[FAIL] %s\n", db.prefix, msg)
		return false, msg
	}

	db.seats[seatNum] = "available"
	msg := fmt.Sprintf("Seat %d cancelled successfully", seatNum)
	fmt.Printf("%s[OK] %s\n", db.prefix, msg)
	return true, msg
}

// ViewSeats returns a copy of all seat statuses.
func (db *SeatDB) ViewSeats() map[int]string {
	db.mu.Lock()
	defer db.mu.Unlock()

	copied := make(map[int]string)
	for k, v := range db.seats {
		copied[k] = v
	}
	return copied
}

// SetSeatStatus directly sets a seat's status (used for peer synchronization).
func (db *SeatDB) SetSeatStatus(seatNum int, status string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if seatNum < 1 || seatNum > types.TotalSeats {
		return
	}
	db.seats[seatNum] = status
	fmt.Printf("%s[SYNC] Seat %d updated to \"%s\" (peer sync)\n", db.prefix, seatNum, status)
}
