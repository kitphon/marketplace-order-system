package domain

// OrderItem is a snapshot. Its name, seller, and unit price do not change when
// the corresponding product changes later.
type OrderItem struct {
	productID string
	sellerID  string
	name      string
	unitPrice Money
	quantity  int
	subtotal  Money
}

func NewOrderItem(
	productID string,
	sellerID string,
	name string,
	unitPrice Money,
	quantity int,
) (OrderItem, error) {
	if productID == "" {
		return OrderItem{}, ErrMissingProductID
	}
	if sellerID == "" {
		return OrderItem{}, ErrMissingSellerID
	}
	if name == "" {
		return OrderItem{}, ErrMissingProductName
	}
	if quantity <= 0 {
		return OrderItem{}, ErrInvalidQuantity
	}
	if unitPrice < 0 {
		return OrderItem{}, ErrNegativeMoney
	}

	subtotal, err := unitPrice.Multiply(quantity)
	if err != nil {
		return OrderItem{}, err
	}

	return OrderItem{
		productID: productID,
		sellerID:  sellerID,
		name:      name,
		unitPrice: unitPrice,
		quantity:  quantity,
		subtotal:  subtotal,
	}, nil
}

func (i OrderItem) ProductID() string { return i.productID }
func (i OrderItem) SellerID() string  { return i.sellerID }
func (i OrderItem) Name() string      { return i.name }
func (i OrderItem) UnitPrice() Money  { return i.unitPrice }
func (i OrderItem) Quantity() int     { return i.quantity }
func (i OrderItem) Subtotal() Money   { return i.subtotal }

func (i OrderItem) validate() error {
	if i.productID == "" {
		return ErrMissingProductID
	}
	if i.sellerID == "" {
		return ErrMissingSellerID
	}
	if i.name == "" {
		return ErrMissingProductName
	}
	if i.quantity <= 0 {
		return ErrInvalidQuantity
	}
	if i.unitPrice < 0 {
		return ErrNegativeMoney
	}
	wantSubtotal, err := i.unitPrice.Multiply(i.quantity)
	if err != nil {
		return err
	}
	if i.subtotal != wantSubtotal {
		return ErrOrderTotalMismatch
	}
	return nil
}
