// Command papermgr is a tag-centric, PDF-first paper manager.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"papermgr/internal/store"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		code := 1
		var ec *exitError
		switch {
		case errors.As(err, &ec):
			code = ec.Code
		case errors.Is(err, store.ErrLocked):
			code = 3
		case isUsageError(err):
			code = 2
		}
		if flagJSON {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"error": err.Error(), "exit_code": code})
		} else {
			fmt.Fprintln(os.Stderr, "papermgr:", err)
		}
		os.Exit(code)
	}
}

type exitError struct {
	Code int
	Err  error
}

func (e *exitError) Error() string { return e.Err.Error() }
func (e *exitError) Unwrap() error { return e.Err }

// isUsageError maps cobra/pflag validation failures to exit code 2.
func isUsageError(err error) bool {
	msg := err.Error()
	for _, marker := range []string{
		"accepts ", "requires at least", "requires at most", "required flag",
		"unknown flag", "unknown command", "invalid argument", "arg(s)",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func codeErr(code int, err error) error {
	if err == nil {
		return nil
	}
	return &exitError{Code: code, Err: err}
}
