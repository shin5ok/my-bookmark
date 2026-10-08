package content

import (
	"context"
	"errors"
	"fmt"
	"net"
)

type APIError struct{ Status int }

func (e *APIError) Error() string { return fmt.Sprintf("Gemini returned HTTP %d", e.Status) }

func Retryable(err error) bool {
	var api *APIError
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &network) ||
		(errors.As(err, &api) && (api.Status == 429 || api.Status >= 500))
}
