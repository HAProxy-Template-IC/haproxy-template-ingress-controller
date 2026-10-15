package validator

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Verdict is a ValidationHandler's judgement of one config.
type Verdict struct {
	Valid  bool
	Errors []string
	// Inconclusive marks Errors as reasons the handler could not judge the
	// config, not defects in it.
	Inconclusive bool
}

func verdictFromErrors(errs []string) Verdict {
	return Verdict{Valid: len(errs) == 0, Errors: errs}
}

// SchemaAcquisitionError reports that watched-resource schemas could not be
// acquired, so typed template access cannot be checked.
type SchemaAcquisitionError struct {
	// Subject names what could not be validated without the schemas.
	Subject string
	Err     error
}

func (e *SchemaAcquisitionError) Error() string {
	return "schema acquisition failed for one or more watched resources (" +
		e.Subject + " cannot be validated without real schemas): " + e.Err.Error()
}

func (e *SchemaAcquisitionError) Unwrap() error { return e.Err }

// Transient reports whether the failure says nothing about the config: the
// apiserver was slow, overloaded, or unreachable.
func (e *SchemaAcquisitionError) Transient() bool {
	err := e.Err
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err) || apierrors.IsInternalError(err) || apierrors.IsUnexpectedServerError(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// verdictFromError turns a setup error into a rejection, or into an
// inconclusive verdict when schemas were transiently unavailable.
func verdictFromError(err error) Verdict {
	var schemaErr *SchemaAcquisitionError
	return Verdict{
		Errors:       []string{err.Error()},
		Inconclusive: errors.As(err, &schemaErr) && schemaErr.Transient(),
	}
}
