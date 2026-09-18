// SPDX-License-Identifier: Apache-2.0
package askpass

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Mode string

const (
	Input       Mode = "input"
	Confirm     Mode = "confirm"
	Notify      Mode = "notify"
	MaxResponse      = 1022
)

type Request struct {
	Prompt string
	Mode   Mode
	Title  string
	Touch  bool
}

func Parse(args []string, hint string) (Request, error) {
	if len(args) > 1 {
		return Request{}, errors.New("expected at most one prompt argument")
	}
	prompt := "Enter SSH passphrase:"
	if len(args) == 1 && args[0] != "" {
		prompt = args[0]
	}
	normalized := strings.Join(strings.Fields(prompt), " ")
	prefix := "Confirm user presence for key"
	touch := normalized == prefix || strings.HasPrefix(normalized, prefix+" ")
	r := Request{Prompt: prompt, Mode: Input, Title: "SSH input", Touch: touch}
	switch {
	case hint == "none", hint == "" && touch:
		r.Mode, r.Title = Notify, "SSH notification"
		if touch {
			r.Title = "Touch your security key"
		}
	case hint == "confirm":
		r.Mode, r.Title = Confirm, "SSH confirmation"
	case strings.HasPrefix(prompt, "Enter PIN for "):
		r.Title = "Security key PIN"
	case strings.HasPrefix(prompt, "Enter passphrase"), strings.HasPrefix(prompt, "Bad passphrase"), len(args) == 0:
		r.Title = "SSH passphrase"
	}
	return r, nil
}

func Validate(value string) error {
	if len(value) > MaxResponse {
		return errors.New("response exceeds 1022 UTF-8 bytes")
	}
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("response must be UTF-8 without NUL or line breaks")
	}
	return nil
}

// WriteResponse does not trim or transform credentials. Short writes are failures.
func WriteResponse(w io.Writer, value string) (int, error) {
	if err := Validate(value); err != nil {
		return 0, err
	}
	buf := append([]byte(value), '\n')
	defer clear(buf)
	n, err := w.Write(buf)
	if err == nil && n != len(buf) {
		err = io.ErrShortWrite
	}
	return n, err
}

type Key struct{ Path, Kind string }

var pinPrompt = regexp.MustCompile(`^Enter PIN for (?:ED25519-SK|ECDSA-SK) key (.+): ?$`)

// CacheKey recognizes only key-specific OpenSSH formats. The returned path still
// needs filesystem resolution and validation before use as a cache identity.
func (r Request) CacheKey() (Key, bool) {
	if r.Mode != Input || strings.ContainsAny(r.Prompt, "\x00\r\n") {
		return Key{}, false
	}
	p := strings.TrimSuffix(r.Prompt, " ")
	if strings.HasPrefix(p, "Enter passphrase for key '") && strings.HasSuffix(p, "':") {
		path := strings.TrimSuffix(strings.TrimPrefix(p, "Enter passphrase for key '"), "':")
		// ssh uses %.100s; a 100-byte path might have been truncated.
		return Key{path, "passphrase"}, path != "" && len(path) < 100
	}
	for _, prefix := range []string{"Enter passphrase for ", "Bad passphrase, try again for "} {
		if strings.HasPrefix(p, prefix) && strings.HasSuffix(p, ":") && len(r.Prompt) < 1023 {
			path := strings.TrimSuffix(strings.TrimPrefix(p, prefix), ":")
			// ssh-add -c appends text indistinguishable from part of a filename.
			if strings.HasSuffix(path, " (will confirm each use)") || path == "(stdin)" || path == "PKCS#11" {
				return Key{}, false
			}
			return Key{path, "passphrase"}, path != ""
		}
	}
	if m := pinPrompt.FindStringSubmatch(r.Prompt); m != nil {
		return Key{m[1], "pin"}, true
	}
	return Key{}, false
}
