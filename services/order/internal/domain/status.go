package domain

type OrderStatus string

const (
	OrderStatusPending           OrderStatus = "PENDING"
	OrderStatusInventoryReserved OrderStatus = "INVENTORY_RESERVED"
	OrderStatusPaymentProcessing OrderStatus = "PAYMENT_PROCESSING"
	OrderStatusConfirmed         OrderStatus = "CONFIRMED"
	OrderStatusCancelled         OrderStatus = "CANCELLED"
)

