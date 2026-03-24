package app

import "errors"

const (
	ExitSuccess   = 0
	ExitConfig    = 2
	ExitExecution = 3
	ExitAssertion = 4
)

var (
	ErrConfig    = errors.New("config error")
	ErrExecution = errors.New("execution error")
	ErrAssertion = errors.New("assertion error")
)
