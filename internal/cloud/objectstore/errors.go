package objectstore

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound           = errors.New("object store resource not found")
	ErrPreconditionFailed = errors.New("object store precondition failed")
	ErrUnauthorized       = errors.New("object store request unauthorized")
	ErrInvalidInput       = errors.New("invalid object store input")
	ErrTemporary          = errors.New("temporary object store failure")
)

// ProviderError keeps provider diagnostics while exposing a stable error kind
// through errors.Is. Err may contain request IDs; it must never contain secrets.
type ProviderError struct {
	Operation string
	Kind      error
	Err       error
}

func (value *ProviderError) Error() string {
	if value == nil {
		return "<nil>"
	}
	if value.Operation == "" {
		return fmt.Sprintf("object store: %v", value.Err)
	}
	return fmt.Sprintf("object store %s: %v", value.Operation, value.Err)
}

func (value *ProviderError) Unwrap() []error {
	if value == nil {
		return nil
	}
	result := make([]error, 0, 2)
	if value.Kind != nil {
		result = append(result, value.Kind)
	}
	if value.Err != nil {
		result = append(result, value.Err)
	}
	return result
}
