package app

import (
	"errors"
	"fmt"
	"io"
)

type exitError struct {
	Code int
	Err  error
}

func (e *exitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *exitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func Execute(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRootCommand(stdin, stdout, stderr, func(req commandRequest) int {
		return NewRuntime(stdout, stderr).Run(req)
	})
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			if exitErr.Err != nil {
				_, _ = fmt.Fprintf(stderr, "error: %v\n", exitErr.Err)
			}
			return exitErr.Code
		}

		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitConfig
	}

	return ExitSuccess
}

func Main(args []string, stdout io.Writer, stderr io.Writer) int {
	return Execute(args, nil, stdout, stderr)
}
