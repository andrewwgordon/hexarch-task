// args.go implements a small, dependency-free option parser for CLI
// subcommands. It supports both "--key value" and "--key=value" forms and
// collects everything else as positional arguments. The stdlib flag package
// is deliberately avoided so subcommands stay simple and predictable.
//
// Public API:
//   - Types: ParsedArgs
//
// Private:
//   - parsing:  parseArgs, newParsedArgs
//   - accessors: hasOpt, getOpt, requiredOpt, parseIntOpt
//   - errors:   cliError
package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsedArgs is the result of turning raw argv tokens into options plus
// positional arguments.
type ParsedArgs struct {
	options    map[string]string
	positional []string
}

// newParsedArgs returns an empty ParsedArgs with the options map allocated.
func newParsedArgs() *ParsedArgs {
	return &ParsedArgs{options: map[string]string{}}
}

// parseArgs turns raw argv tokens (e.g. ["--title","x","--desc=y"]) into
// option/key-value pairs and positional arguments.
func parseArgs(args []string) (ParsedArgs, error) {
	parsed := newParsedArgs()

	i := 0
	for i < len(args) {
		tok := args[i]

		if strings.HasPrefix(tok, "--") {
			key := strings.TrimPrefix(tok, "--")

			var value string
			if eq := strings.IndexByte(key, '='); eq >= 0 {
				// --key=value form
				value = key[eq+1:]
				key = key[:eq]
				parsed.options[key] = value
			} else {
				// --key value form
				i++
				if i >= len(args) {
					return ParsedArgs{}, cliError{msg: "missing value for --" + key}
				}
				parsed.options[key] = args[i]
			}
		} else {
			parsed.positional = append(parsed.positional, tok)
		}
		i++
	}
	return *parsed, nil
}

// hasOpt reports whether the option key was provided.
func hasOpt(opts map[string]string, key string) bool {
	_, ok := opts[key]
	return ok
}

// getOpt returns the value for key, or the fallback if absent.
func getOpt(opts map[string]string, key, fallback string) string {
	if v, ok := opts[key]; ok {
		return v
	}
	return fallback
}

// requiredOpt returns the option value or an error if missing.
func requiredOpt(opts map[string]string, key, what string) (string, error) {
	if v, ok := opts[key]; ok {
		return v, nil
	}
	return "", cliError{msg: fmt.Sprintf("missing --%s (%s)", key, what)}
}

// parseIntOpt parses an option as an int, returning fallback when absent.
func parseIntOpt(opts map[string]string, key string, fallback int) (int, error) {
	if !hasOpt(opts, key) {
		return fallback, nil
	}
	n, err := strconv.Atoi(opts[key])
	if err != nil {
		return fallback, cliError{msg: fmt.Sprintf("invalid integer for --%s: %q", key, opts[key])}
	}
	return n, nil
}

// cliError is the local error type for argument-parsing failures. It is
// distinct from domain errors: it signals usage problems before any service
// call is made.
type cliError struct{ msg string }

func (e cliError) Error() string { return e.msg }
