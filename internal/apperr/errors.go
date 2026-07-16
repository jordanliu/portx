package apperr

import (
	"errors"
	"fmt"
)

const (
	ExitOK             = 0
	ExitInvalidArgs    = 2
	ExitOrigin         = 3
	ExitConflict       = 4
	ExitAuth           = 5
	ExitProvision      = 6
	ExitCloudflared    = 7
	ExitDaemon         = 8
	ExitCleanupWarning = 9
)

type CodeError struct {
	Code    int
	Message string
	Err     error
}

func (e *CodeError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *CodeError) Unwrap() error {
	return e.Err
}

func New(code int, message string) *CodeError {
	return &CodeError{Code: code, Message: message}
}

func Wrap(code int, message string, err error) *CodeError {
	return &CodeError{Code: code, Message: message, Err: err}
}

func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ce *CodeError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return 1
}
