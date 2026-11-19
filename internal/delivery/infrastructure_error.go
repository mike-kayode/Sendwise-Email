package delivery

import (
	"errors"
	"fmt"
)

type InfrastructureError struct {
	Err error
}

func (e *InfrastructureError) Error() string {
	return fmt.Sprintf("smtp infrastructure failure: %v", e.Err)
}

func (e *InfrastructureError) Unwrap() error {
	return e.Err
}

func IsInfrastructureError(err error) bool {
	var infrastructureError *InfrastructureError
	return errors.As(err, &infrastructureError)
}

func infrastructureError(err error) error {
	if err == nil {
		return nil
	}
	return &InfrastructureError{Err: err}
}
