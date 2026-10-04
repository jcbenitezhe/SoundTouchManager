// Package logredact wraps a slog.Handler so access tokens carried in stream
// URLs (TuneIn's accessKey, CDN signatures) never reach a log line, whichever
// component logs the URL and whether it arrives as a string, an error or a
// group. The agent log ends up in diagnostic bundles, so this is the one
// place that has to be right rather than every call site.
package logredact

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jcbenitezhe/SoundTouchManager/tunein"
)

type handler struct{ next slog.Handler }

// New returns a handler that redacts token parameters before passing records
// on to next.
func New(next slog.Handler) slog.Handler { return &handler{next: next} }

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(as))
	for i, a := range as {
		red[i] = redactAttr(a)
	}
	return &handler{next: h.next.WithAttrs(red)}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{next: h.next.WithGroup(name)}
}

func redact(s string) string {
	if strings.IndexByte(s, '=') < 0 {
		return s
	}
	return tunein.Redact(s)
}

func redactAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		if s := v.String(); strings.IndexByte(s, '=') >= 0 && tunein.HasToken(s) {
			return slog.String(a.Key, tunein.Redact(s))
		}
	case slog.KindGroup:
		g := v.Group()
		red := make([]any, len(g))
		for i, ga := range g {
			red[i] = redactAttr(ga)
		}
		return slog.Group(a.Key, red...)
	case slog.KindAny:
		var s string
		switch x := v.Any().(type) {
		case error:
			s = x.Error()
		case fmt.Stringer:
			s = x.String()
		default:
			return slog.Attr{Key: a.Key, Value: v}
		}
		if tunein.HasToken(s) {
			return slog.String(a.Key, tunein.Redact(s))
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}
