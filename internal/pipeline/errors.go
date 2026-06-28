package pipeline

import (
	"errors"

	"github.com/qiuyier/Z-Courier/internal/protocol"
)

type Error struct {
	Code   protocol.AckCode
	Reason string
	Err    error
}

func Reject(code protocol.AckCode, err error) error {
	if err == nil {
		return nil
	}

	return &Error{Code: code, Err: err}
}

func RejectWithReason(code protocol.AckCode, reason string, err error) error {
	if err == nil {
		return nil
	}

	return &Error{Code: code, Reason: reason, Err: err}
}

func (e *Error) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}

	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}

func AckError(err error) (protocol.AckCode, string) {
	if err == nil {
		return protocol.AckAccepted, ""
	}

	var pipelineErr *Error
	if errors.As(err, &pipelineErr) {
		if pipelineErr.Reason != "" {
			return pipelineErr.Code, pipelineErr.Reason
		}
		return pipelineErr.Code, pipelineErr.Error()
	}

	return protocol.AckRejected, err.Error()
}
