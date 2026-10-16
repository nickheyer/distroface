package mailer

import (
	"fmt"
	"html"
	"strings"
)

// Fields every templated mail carries
type Template struct {
	Instance string
	Title    string
	Intro    string
	Action   string
	URL      string
	Footer   string
}

// Renders text and html bodies with a shared layout
func Render(t Template) (string, string) {
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n\n", t.Title)
	if t.Intro != "" {
		fmt.Fprintf(&text, "%s\n\n", t.Intro)
	}
	if t.URL != "" {
		label := t.Action
		if label == "" {
			label = "Open"
		}
		fmt.Fprintf(&text, "%s: %s\n\n", label, t.URL)
	}
	if t.Footer != "" {
		fmt.Fprintf(&text, "--\n%s\n", t.Footer)
	}
	fmt.Fprintf(&text, "%s\n", t.Instance)

	var h strings.Builder
	h.WriteString(`<!doctype html><html><body style="font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#1f2937;background:#f9fafb;margin:0;padding:24px">`)
	h.WriteString(`<div style="max-width:560px;margin:0 auto;background:#ffffff;border:1px solid #e5e7eb;border-radius:8px;padding:24px">`)
	fmt.Fprintf(&h, `<h2 style="margin:0 0 16px;font-size:18px">%s</h2>`, html.EscapeString(t.Title))
	if t.Intro != "" {
		fmt.Fprintf(&h, `<p style="margin:0 0 16px;white-space:pre-wrap;line-height:1.5">%s</p>`, html.EscapeString(t.Intro))
	}
	if t.URL != "" {
		label := t.Action
		if label == "" {
			label = "Open"
		}
		fmt.Fprintf(&h, `<p style="margin:0 0 16px"><a href="%s" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;padding:10px 16px;border-radius:6px">%s</a></p>`, html.EscapeString(t.URL), html.EscapeString(label))
		fmt.Fprintf(&h, `<p style="margin:0 0 16px;font-size:12px;color:#6b7280;word-break:break-all">%s</p>`, html.EscapeString(t.URL))
	}
	if t.Footer != "" {
		fmt.Fprintf(&h, `<p style="margin:16px 0 0;font-size:12px;color:#6b7280;border-top:1px solid #e5e7eb;padding-top:12px">%s</p>`, html.EscapeString(t.Footer))
	}
	fmt.Fprintf(&h, `<p style="margin:8px 0 0;font-size:12px;color:#9ca3af">%s</p>`, html.EscapeString(t.Instance))
	h.WriteString(`</div></body></html>`)
	return text.String(), h.String()
}
