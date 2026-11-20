package hostaddr

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ParseOrigin checks an origin of scheme (https: default port 443, http:
// 80) without path, query, fragment or credentials and returns its
// canonical form (lower case, the default port omitted) and the Host
// header values that address it.
func ParseOrigin(s, scheme string) (origin string, hosts []string, err error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != scheme || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" {
		return "", nil, fmt.Errorf("public origin must be %s://HOST[:PORT] without a path", scheme)
	}
	def := "443"
	if scheme == "http" {
		def = "80"
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", nil, errors.New("public origin port must be 1..65535")
		}
	}
	hostport := func(p string) string {
		if p == "" {
			if strings.Contains(host, ":") {
				return "[" + host + "]"
			}
			return host
		}
		return net.JoinHostPort(host, p)
	}
	if port == "" || port == def {
		return scheme + "://" + hostport(""), []string{hostport(""), hostport(def)}, nil
	}
	return scheme + "://" + hostport(port), []string{hostport(port)}, nil
}
