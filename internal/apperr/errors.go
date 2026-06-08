// Package apperr defines the domain error types shared across Formation's
// service clients and tool handlers. These mirror the exceptions defined in
// the original Python implementation and are matched with errors.As so callers
// can react to a condition without string-matching error messages.
package apperr

import (
	"errors"
	"fmt"
)

// NotFoundError indicates a requested resource does not exist.
type NotFoundError struct {
	Resource string
	ID       string
}

func (e *NotFoundError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("%s '%s' not found", e.Resource, e.ID)
	}
	return fmt.Sprintf("%s not found", e.Resource)
}

// NotFound builds a NotFoundError. A blank id is allowed.
func NotFound(resource, id string) *NotFoundError {
	return &NotFoundError{Resource: resource, ID: id}
}

// ValidationError indicates the caller supplied an invalid value.
type ValidationError struct {
	Message string
	Field   string
}

func (e *ValidationError) Error() string { return e.Message }

// Validation builds a ValidationError for the named field.
func Validation(field, message string) *ValidationError {
	return &ValidationError{Message: message, Field: field}
}

// BadRequestError indicates an otherwise valid request that cannot be performed.
type BadRequestError struct{ Message string }

func (e *BadRequestError) Error() string { return e.Message }

// BadRequest builds a BadRequestError.
func BadRequest(message string) *BadRequestError {
	return &BadRequestError{Message: message}
}

// PermissionDeniedError indicates the user lacks access to a resource.
type PermissionDeniedError struct{ Message string }

func (e *PermissionDeniedError) Error() string {
	if e.Message == "" {
		return "Access denied"
	}
	return e.Message
}

// PermissionDenied builds a PermissionDeniedError with the default message.
func PermissionDenied() *PermissionDeniedError { return &PermissionDeniedError{} }

// ServiceUnavailableError indicates a downstream service is not configured.
type ServiceUnavailableError struct{ Service string }

func (e *ServiceUnavailableError) Error() string {
	return fmt.Sprintf("%s service not configured", e.Service)
}

// ServiceUnavailable builds a ServiceUnavailableError.
func ServiceUnavailable(service string) *ServiceUnavailableError {
	return &ServiceUnavailableError{Service: service}
}

// AsNotFound reports whether err is a NotFoundError.
func AsNotFound(err error) bool {
	var t *NotFoundError
	return errors.As(err, &t)
}
