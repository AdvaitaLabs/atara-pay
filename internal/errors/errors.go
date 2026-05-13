// Package errors defines Atara-Pay's typed error sentinels.
package errors

import (
	"errors"
	"fmt"
)

var (
	ErrUnsupported = errors.New("operation not supported by this rail")
	ErrNotFound    = errors.New("resource not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrBadRequest  = errors.New("bad request")
	ErrUpstream    = errors.New("upstream rail error")
)

// UpstreamError wraps a provider-side failure with the original HTTP status
// and body for debugging.
type UpstreamError struct {
	Rail   string
	Status int
	Body   string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream %s returned %d: %s", e.Rail, e.Status, e.Body)
}

func (e *UpstreamError) Unwrap() error { return ErrUpstream }
