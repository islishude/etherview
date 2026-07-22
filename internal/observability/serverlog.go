package observability

import (
	"log"
	"log/slog"
)

// HTTPServerErrorLog replaces net/http's plaintext internal logger. net/http
// supplies a fully formatted line that can contain a recovered panic value,
// stack, request target, or peer input, so the adapter deliberately discards
// the bytes and emits only a stable structured event.
func HTTPServerErrorLog(logger *slog.Logger) *log.Logger {
	if logger == nil {
		logger = NewLogger(LoggerOptions{})
	}
	return log.New(stableHTTPServerErrorWriter{logger: logger}, "", 0)
}

type stableHTTPServerErrorWriter struct {
	logger *slog.Logger
}

func (writer stableHTTPServerErrorWriter) Write(message []byte) (int, error) {
	writer.logger.Error("HTTP server internal error",
		"error_code", "http_server_error",
	)
	return len(message), nil
}
