package exchange

import "errors"

var (
	// ErrReadOnly is returned by data-only connectors (e.g. Yahoo chart) for trading methods.
	ErrReadOnly = errors.New("read-only data connector: no order routing")
	// ErrAuthRequired means API key/secret (or account id) is missing or invalid.
	ErrAuthRequired = errors.New("credentials required for this operation")
	// ErrSidecar means the CCXT sidecar returned an error or unreachable.
	ErrSidecar = errors.New("ccxt sidecar error")
)
