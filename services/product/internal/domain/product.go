package domain

import "errors"

var (
	ErrMissingProductID   = errors.New("product id is required")
	ErrMissingSellerID    = errors.New("seller id is required")
	ErrMissingProductName = errors.New("product name is required")
	ErrNegativeUnitPrice  = errors.New("unit price cannot be negative")
	ErrMissingCurrency    = errors.New("currency is required")
)

// Product is an immutable product snapshot exposed by the Product Service.
type Product struct {
	productID      string
	sellerID       string
	name           string
	unitPriceMinor int64
	currency       string
	active         bool
}

func NewProduct(
	productID string,
	sellerID string,
	name string,
	unitPriceMinor int64,
	currency string,
	active bool,
) (Product, error) {
	if productID == "" {
		return Product{}, ErrMissingProductID
	}
	if sellerID == "" {
		return Product{}, ErrMissingSellerID
	}
	if name == "" {
		return Product{}, ErrMissingProductName
	}
	if unitPriceMinor < 0 {
		return Product{}, ErrNegativeUnitPrice
	}
	if currency == "" {
		return Product{}, ErrMissingCurrency
	}
	return Product{
		productID:      productID,
		sellerID:       sellerID,
		name:           name,
		unitPriceMinor: unitPriceMinor,
		currency:       currency,
		active:         active,
	}, nil
}

func (p Product) ProductID() string     { return p.productID }
func (p Product) SellerID() string      { return p.sellerID }
func (p Product) Name() string          { return p.name }
func (p Product) UnitPriceMinor() int64 { return p.unitPriceMinor }
func (p Product) Currency() string      { return p.currency }
func (p Product) Active() bool          { return p.active }
