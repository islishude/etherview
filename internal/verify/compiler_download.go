package verify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

// CompilerDownloadError carries only controlled labels, never remote diagnostics.
type CompilerDownloadError struct {
	Code       ErrorCode
	RetryAfter time.Duration
}

func (err CompilerDownloadError) Error() string { return string(err.Code) }

func (err CompilerDownloadError) retryable() bool {
	return err.Code == ErrorCompilerDownloadUnavailable || err.Code == ErrorCompilerDownloadTimeout || err.Code == ErrorCompilerDownloadRateLimited
}

func compilerDownloadFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	code := ErrorCompilerDownloadUnavailable
	var network net.Error
	if errors.Is(err, errOutboundPolicy) {
		code = ErrorCompilerDownloadRejected
	} else if errors.As(err, &network) && network.Timeout() {
		code = ErrorCompilerDownloadTimeout
	}
	return CompilerDownloadError{Code: code}
}

func compilerDownloadHTTPFailure(response *http.Response) error {
	code := ErrorCompilerDownloadRejected
	if response.StatusCode >= 500 && response.StatusCode < 600 {
		code = ErrorCompilerDownloadUnavailable
	}
	if response.StatusCode == http.StatusRequestTimeout {
		code = ErrorCompilerDownloadTimeout
	}
	if response.StatusCode == http.StatusTooManyRequests {
		code = ErrorCompilerDownloadRateLimited
	}
	delay := time.Duration(0)
	if seconds, err := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 64); err == nil {
		delay = time.Duration(min(max(seconds, 0), 30)) * time.Second
	} else if date, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
		delay = min(max(time.Until(date), 0), 30*time.Second)
	}
	return CompilerDownloadError{Code: code, RetryAfter: delay}
}
