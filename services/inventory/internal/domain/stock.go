package domain

import "errors"

var (
	ErrMissingProductID     = errors.New("product id is required")
	ErrInvalidStockQuantity = errors.New("stock quantity cannot be negative")
)

type Stock struct {
	productID string
	available int
}

func NewStock(productID string, available int) (Stock, error) {
	if productID == "" {
		return Stock{}, ErrMissingProductID
	}
	if available < 0 {
		return Stock{}, ErrInvalidStockQuantity
	}
	return Stock{productID: productID, available: available}, nil
}

func (s Stock) ProductID() string { return s.productID }
func (s Stock) Available() int    { return s.available }
