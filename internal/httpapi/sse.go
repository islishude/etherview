package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// sseStream converts the server-wide response timeout into a per-write budget.
// The deadline is cleared between frames so an otherwise healthy idle stream
// is not terminated merely because no chain event arrived within WriteTimeout.
type sseStream struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
}

func newSSEStream(writer http.ResponseWriter, timeout time.Duration) (*sseStream, error) {
	if writer == nil || timeout <= 0 {
		return nil, errors.New("SSE stream writer or timeout is invalid")
	}
	controller := http.NewResponseController(writer)
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("clear SSE idle write deadline: %w", err)
	}
	return &sseStream{writer: writer, controller: controller, timeout: timeout}, nil
}

func (stream *sseStream) flush() error {
	return stream.withWriteDeadline(stream.controller.Flush)
}

func (stream *sseStream) write(format string, arguments ...any) error {
	return stream.withWriteDeadline(func() error {
		if _, err := fmt.Fprintf(stream.writer, format, arguments...); err != nil {
			return err
		}
		return stream.controller.Flush()
	})
}

func (stream *sseStream) withWriteDeadline(operation func() error) error {
	if stream == nil || stream.controller == nil || operation == nil {
		return errors.New("SSE stream is not configured")
	}
	if err := stream.controller.SetWriteDeadline(time.Now().Add(stream.timeout)); err != nil {
		return err
	}
	operationErr := operation()
	clearErr := stream.controller.SetWriteDeadline(time.Time{})
	return errors.Join(operationErr, clearErr)
}
