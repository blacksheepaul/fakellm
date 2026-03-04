package queue

import "errors"

// ErrFull is returned when the queue has no room for a new request.
var ErrFull = errors.New("queue full")

// ErrTimeout is returned when a request exceeds its queue-wait budget.
var ErrTimeout = errors.New("queue wait timeout")
