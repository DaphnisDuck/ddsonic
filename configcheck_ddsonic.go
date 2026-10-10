package main

// ddsonic: what must hold before the player starts, checked before anything
// is loaded or created. The IPC socket's path must be usable
// (sockpath_ddsonic.go), and a config.toml that exists must be well-formed:
// a broken one used to start the player on defaults without a word, and now
// stops it (owner, 2026-10-09). No config file is not an error.
//
// Only the player's start is checked. The remote commands read no config,
// and setup works on a broken file by design: it edits text.

import (
	"errors"
	"fmt"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/appmeta"
)

// startupChecks is run's first step.
func startupChecks() error {
	if err := checkSocketPath(); err != nil {
		return err
	}
	return checkConfigFile()
}

// checkConfigFile refuses a config.toml that is malformed, naming the file,
// the line and column, and what is wrong there. The file is left as it is.
func checkConfigFile() error {
	err := config.CheckSyntax()
	var syntax *config.SyntaxError
	switch {
	case errors.As(err, &syntax):
		return configSyntaxError{syntax}
	case err != nil:
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// configSyntaxError is a config.SyntaxError as the user reads it.
type configSyntaxError struct{ cause *config.SyntaxError }

func (e configSyntaxError) Unwrap() error { return e.cause }

func (e configSyntaxError) Error() string {
	name := appmeta.ClientName()
	return fmt.Sprintf("%s cannot read its config file: %s\n"+
		"%s did not start, and the file was not changed. Correct it at line %d (or just before: an unclosed line is noticed on the next one), or move the file away to start on the defaults.",
		name, e.cause, name, e.cause.Line)
}
