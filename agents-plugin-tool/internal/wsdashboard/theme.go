package wsdashboard

import (
	"html/template"
	"net/http"
)

// themeCookie is the browser cookie holding the gear menu's theme choice:
// "light" or "dark". No cookie means auto, the page following the OS. The
// server reads it so a page renders its root data-theme from the first byte,
// with no flash of the wrong theme; the page script writes it.
const themeCookie = "ws_dashboard_theme"

// requestTheme is the theme a request pins: "light" or "dark" when the cookie
// says so, else "" (auto). Any other cookie value is auto.
func requestTheme(r *http.Request) string {
	c, err := r.Cookie(themeCookie)
	if err != nil {
		return ""
	}
	switch c.Value {
	case "light", "dark":
		return c.Value
	}
	return ""
}

// firstPaintStyle returns an inline <style> block placed early in <head>
// so the shell and every framed page paint their theme background and
// matching color-scheme before external stylesheets load. Without it, a
// freshly loaded dark-theme iframe paints an opaque white canvas while its
// linked stylesheets are still uncached.
func firstPaintStyle() template.HTML {
	return template.HTML(`<style id="ws-first-paint">` +
		`html{background:var(--wb-bg,` + themeColorLight + `);color-scheme:light dark}` +
		`@media(prefers-color-scheme:dark){html:not([data-theme="light"]){background:var(--wb-bg,` + themeColorDark + `)}}` +
		`html[data-theme="dark"]{background:var(--wb-bg,` + themeColorDark + `);color-scheme:dark}` +
		`html[data-theme="light"]{color-scheme:light}` +
		`</style>`)
}
