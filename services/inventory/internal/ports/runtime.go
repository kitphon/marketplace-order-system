package ports

import "time"

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() (string, error)
}
