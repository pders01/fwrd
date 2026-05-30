package web

import (
	"net/http"
	"net/url"
	"strings"
)

// FlashMsg is a one-shot notice rendered once on the next page the browser
// lands on after a Post/Redirect/Get. Fields are exported so layout.html can
// render them. Kind drives styling: "error" or "notice".
type FlashMsg struct {
	Kind string
	Text string
}

const (
	flashError  = "error"
	flashNotice = "notice"

	flashCookie  = "fwrd_flash"
	flashMaxText = 240 // cap so a long upstream error can't bloat the cookie
)

// pageBase is embedded in every page's view model so layout.html can render a
// flash uniformly. Its zero value (nil Flash) renders nothing.
type pageBase struct {
	Flash *FlashMsg
}

// setFlash stores a one-shot message in a short-lived cookie to survive the
// PRG redirect. SameSite=Lax and the same-origin guard on the POST keep it
// from being planted cross-site; it is read and cleared on the next render.
func setFlash(w http.ResponseWriter, kind, text string) {
	if len(text) > flashMaxText {
		text = text[:flashMaxText] + "…"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookie,
		Value:    url.QueryEscape(kind) + "." + url.QueryEscape(text),
		Path:     "/",
		MaxAge:   30,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// takeFlash returns and clears the pending flash, if any. Clearing means the
// message shows exactly once even across a reload. Returns nil when absent or
// malformed.
func takeFlash(w http.ResponseWriter, r *http.Request) *FlashMsg {
	c, err := r.Cookie(flashCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	// Expire the cookie regardless of how it parses, so a malformed value
	// can't stick around.
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1})

	kindRaw, textRaw, ok := strings.Cut(c.Value, ".")
	if !ok {
		return nil
	}
	kind, err1 := url.QueryUnescape(kindRaw)
	text, err2 := url.QueryUnescape(textRaw)
	if err1 != nil || err2 != nil || text == "" {
		return nil
	}
	if kind != flashError && kind != flashNotice {
		kind = flashNotice
	}
	return &FlashMsg{Kind: kind, Text: text}
}
