// Package hubapi defines the small Hub surface available to a signed-in GM.
package hubapi

const BrowserPrefix = "/api/v1/hub/"
const PrivatePrefix = "/api/internal/hub/"

func Allowed(method, route string) bool {
	switch method {
	case "GET":
		return route == "state" || route == "news"
	case "POST":
		return route == "news/entries"
	case "PUT":
		return route == "news" || route == "source" || route == "connection"
	}
	return false
}
