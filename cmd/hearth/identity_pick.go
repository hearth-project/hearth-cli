package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/client"
)

// pickIdentity asks which account to use when the local identity is
// ambiguous: every user name this machine knows is listed (as the
// company's dev-machine tools do), or the user types an email. Without a
// terminal it fails with the choices in the message.
func pickIdentity(home string) (string, error) {
	choices := client.IdentityChoices(home)
	if !isTTY() {
		if len(choices) == 0 {
			return "", errors.New("no local identity found on this machine; pass --email <you@example.com>")
		}
		return "", fmt.Errorf("several people are known on this machine (%s); pass --email <you@example.com>", choiceList(choices))
	}
	return chooseIdentity(choices, askLine, os.Stderr)
}

func chooseIdentity(choices []client.LocalIdentity, ask func(string) (string, bool), w io.Writer) (string, error) {
	if len(choices) > 0 {
		fmt.Fprintln(w, "      which account is yours? (accounts known on this machine)")
		for i, c := range choices {
			fmt.Fprintf(w, "        %d) %s  (%s)\n", i+1, choiceName(c), c.Source)
		}
	}
	prompt := "      your account email (new or existing): "
	if len(choices) > 0 {
		prompt = "      number, or your account email: "
	}
	for tries := 0; tries < 3; tries++ {
		a, ok := ask(prompt)
		if !ok {
			break
		}
		if n, err := strconv.Atoi(a); err == nil && n >= 1 && n <= len(choices) {
			c := choices[n-1]
			if c.Email != "" {
				return c.Email, nil
			}
			if a, ok = ask("      email for " + c.Username + ": "); ok && strings.Contains(a, "@") {
				return a, nil
			}
			continue
		}
		if strings.Contains(a, "@") {
			return a, nil
		}
	}
	return "", errors.New("no account chosen; run again with --email <you@example.com>")
}

func choiceName(c client.LocalIdentity) string {
	if c.Email != "" {
		return c.Email
	}
	return c.Username
}

func choiceList(cs []client.LocalIdentity) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = choiceName(c)
	}
	return strings.Join(names, ", ")
}
