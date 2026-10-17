package utils

import "net/url"

// Reports whether s is an absolute http or https url with a host
func IsWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
