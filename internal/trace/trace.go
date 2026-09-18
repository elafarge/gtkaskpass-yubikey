// SPDX-License-Identifier: Apache-2.0
package trace

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

type Logger struct {
	log     *slog.Logger
	secrets bool
}

func New(mode string, w io.Writer) (*Logger, error) {
	l := &Logger{}
	switch mode {
	case "", "off":
		return l, nil
	case "metadata", "secrets":
		l.log = slog.New(slog.NewTextHandler(w, nil)).With("pid", os.Getpid())
		l.secrets = mode == "secrets"
		return l, nil
	default:
		return nil, fmt.Errorf("GTKASKPASS_TRACE must be off, metadata, or secrets")
	}
}

func (l *Logger) Event(event string, attrs ...any) {
	if l != nil && l.log != nil {
		l.log.Info(event, attrs...)
	}
}

func (l *Logger) Response(value string, secret bool) {
	if l == nil || l.log == nil {
		return
	}
	if secret && !l.secrets {
		l.Event("response", "bytes", len(value)+1, "value", "[redacted]")
		return
	}
	l.Event("response", "bytes", len(value)+1, "value", fmt.Sprintf("%q", value+"\n"))
}
