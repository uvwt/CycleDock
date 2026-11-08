package agentconfig

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ValidateEndpoint(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("endpoint must be an absolute URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("endpoint cannot contain user info, query, or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return errors.New("endpoint must include a valid host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsDisallowedTargetIP(ip, true) {
			return errors.New("endpoint cannot target private, link-local, or reserved IP addresses")
		}
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return nil
		}
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return errors.New("endpoint must use HTTPS, except localhost/loopback development")
}

// IsDisallowedTargetIP 判断目标 IP 是否属于禁止探测的私网、链路本地、多播或未指定地址。
// allowLoopback 控制是否放行本地回环地址（127.0.0.0/8、::1）。
func IsDisallowedTargetIP(ip net.IP, allowLoopback bool) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() {
		return !allowLoopback
	}
	return ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func ValidEnvName(name string) bool {
	return envNamePattern.MatchString(strings.TrimSpace(name))
}
