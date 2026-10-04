package domain

import (
	"errors"
	"math"
)

var (
	ErrNegativeMoney = errors.New("money cannot be negative")
	ErrMoneyOverflow = errors.New("money arithmetic overflow")
)

// Money stores an amount in the currency's minor unit. For THB, 200000 means
// 2,000.00 baht. Floating point values are deliberately avoided.
type Money int64

func NewMoney(minorUnits int64) (Money, error) {
	if minorUnits < 0 {
		return 0, ErrNegativeMoney
	}
	return Money(minorUnits), nil
}

func (m Money) MinorUnits() int64 {
	return int64(m)
}

func (m Money) Multiply(quantity int) (Money, error) {
	if quantity <= 0 {
		return 0, ErrInvalidQuantity
	}
	if m > Money(math.MaxInt64/int64(quantity)) {
		return 0, ErrMoneyOverflow
	}
	return m * Money(quantity), nil
}

func AddMoney(left, right Money) (Money, error) {
	if right > 0 && left > Money(math.MaxInt64)-right {
		return 0, ErrMoneyOverflow
	}
	return left + right, nil
}

