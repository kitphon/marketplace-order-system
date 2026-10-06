package domain

import (
	"errors"
	"time"
)

var (
	ErrMissingReservationID = errors.New("reservation id is required")
	ErrMissingOrderID       = errors.New("order id is required")
	ErrEmptyReservation     = errors.New("reservation must contain at least one item")
	ErrInvalidQuantity      = errors.New("item quantity must be greater than zero")
	ErrInvalidExpiry        = errors.New("reservation expiry is required")
)

type ReservationStatus string

const (
	ReservationStatusReserved ReservationStatus = "RESERVED"
	ReservationStatusExpired  ReservationStatus = "EXPIRED"
)

type ReservationItem struct {
	productID string
	quantity  int
}

func NewReservationItem(productID string, quantity int) (ReservationItem, error) {
	if productID == "" {
		return ReservationItem{}, ErrMissingProductID
	}
	if quantity <= 0 {
		return ReservationItem{}, ErrInvalidQuantity
	}
	return ReservationItem{productID: productID, quantity: quantity}, nil
}

func (item ReservationItem) ProductID() string { return item.productID }
func (item ReservationItem) Quantity() int     { return item.quantity }

type Reservation struct {
	id        string
	orderID   string
	items     []ReservationItem
	expiresAt time.Time
	status    ReservationStatus
}

func NewReservation(
	id string,
	orderID string,
	items []ReservationItem,
	expiresAt time.Time,
) (*Reservation, error) {
	if id == "" {
		return nil, ErrMissingReservationID
	}
	if orderID == "" {
		return nil, ErrMissingOrderID
	}
	if len(items) == 0 {
		return nil, ErrEmptyReservation
	}
	if expiresAt.IsZero() {
		return nil, ErrInvalidExpiry
	}
	return &Reservation{
		id:        id,
		orderID:   orderID,
		items:     append([]ReservationItem(nil), items...),
		expiresAt: expiresAt.UTC(),
		status:    ReservationStatusReserved,
	}, nil
}

func (reservation *Reservation) ID() string                { return reservation.id }
func (reservation *Reservation) OrderID() string           { return reservation.orderID }
func (reservation *Reservation) ExpiresAt() time.Time      { return reservation.expiresAt }
func (reservation *Reservation) Status() ReservationStatus { return reservation.status }

func (reservation *Reservation) Items() []ReservationItem {
	return append([]ReservationItem(nil), reservation.items...)
}

func (reservation *Reservation) Expire() bool {
	if reservation.status == ReservationStatusExpired {
		return false
	}
	reservation.status = ReservationStatusExpired
	return true
}

func (reservation *Reservation) Clone() *Reservation {
	clone := *reservation
	clone.items = append([]ReservationItem(nil), reservation.items...)
	return &clone
}
