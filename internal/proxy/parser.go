package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type ParseError struct {
	Line  int    `json:"line"`
	Input string `json:"input"`
	Error string `json:"error"`
}

func Parse(raw string) (Proxy, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return Proxy{}, errors.New("empty proxy line")
	}

	if strings.Contains(input, "://") {
		return parseURL(input)
	}

	if strings.Contains(input, "@") {
		p, ok, firstErr := parseUserPassAtHostPort(input)
		if ok && firstErr == nil {
			return p, nil
		}
		p, ok, secondErr := parseHostPortAtUserPass(input)
		if ok && secondErr == nil {
			return p, nil
		}
		if firstErr != nil {
			return Proxy{}, firstErr
		}
		if secondErr != nil {
			return Proxy{}, secondErr
		}
	}
	if p, ok, err := parseFourPart(input); ok || err != nil {
		return p, err
	}
	if p, err := parseHostPort(input, "socks5", "", "", input); err == nil {
		return p, nil
	}

	return Proxy{}, errors.New("unrecognized proxy format")
}

func ParseLines(text string) ([]Proxy, []ParseError) {
	lines := strings.Split(text, "\n")
	proxies := make([]Proxy, 0, len(lines))
	parseErrors := make([]ParseError, 0)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		p, err := Parse(trimmed)
		if err != nil {
			parseErrors = append(parseErrors, ParseError{
				Line:  i + 1,
				Input: trimmed,
				Error: err.Error(),
			})
			continue
		}
		proxies = append(proxies, p)
	}
	return proxies, parseErrors
}

func parseURL(input string) (Proxy, error) {
	u, err := url.Parse(input)
	if err != nil {
		return Proxy{}, fmt.Errorf("invalid URL: %w", err)
	}
	scheme := CanonicalScheme(u.Scheme)
	if scheme == "" {
		scheme = "socks5"
	}
	if !knownScheme(scheme) {
		return Proxy{}, fmt.Errorf("unsupported scheme %q", scheme)
	}
	host := u.Hostname()
	port, err := parsePort(u.Port())
	if err != nil {
		return Proxy{}, err
	}
	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	return Proxy{
		Scheme: scheme,
		Host:   host,
		Port:   port,
		User:   user,
		Pass:   pass,
		Raw:    input,
	}, nil
}

func parseUserPassAtHostPort(input string) (Proxy, bool, error) {
	idx := strings.LastIndex(input, "@")
	if idx < 0 {
		return Proxy{}, false, nil
	}
	cred := input[:idx]
	hostPort := input[idx+1:]
	parts := strings.SplitN(cred, ":", 2)
	if len(parts) != 2 || parts[0] == "" {
		return Proxy{}, true, errors.New("invalid credentials before @")
	}
	p, err := parseHostPort(hostPort, "socks5", parts[0], parts[1], input)
	return p, true, err
}

func parseHostPortAtUserPass(input string) (Proxy, bool, error) {
	idx := strings.LastIndex(input, "@")
	if idx < 0 {
		return Proxy{}, false, nil
	}
	hostPort := input[:idx]
	cred := input[idx+1:]
	parts := strings.SplitN(cred, ":", 2)
	if len(parts) != 2 || parts[0] == "" {
		return Proxy{}, true, errors.New("invalid credentials after @")
	}
	p, err := parseHostPort(hostPort, "socks5", parts[0], parts[1], input)
	return p, true, err
}

func parseFourPart(input string) (Proxy, bool, error) {
	parts := strings.Split(input, ":")
	if len(parts) != 4 {
		return Proxy{}, false, nil
	}
	secondPort := validPort(parts[1])
	fourthPort := validPort(parts[3])
	if secondPort && fourthPort {
		return Proxy{}, true, errors.New("ambiguous 4-part format; use user:pass@host:port or socks5://user:pass@host:port")
	}
	if secondPort {
		port, _ := strconv.Atoi(parts[1])
		return Proxy{
			Scheme: "socks5",
			Host:   parts[0],
			Port:   port,
			User:   parts[2],
			Pass:   parts[3],
			Raw:    input,
		}, true, validateHost(parts[0])
	}
	if fourthPort {
		port, _ := strconv.Atoi(parts[3])
		return Proxy{
			Scheme: "socks5",
			Host:   parts[2],
			Port:   port,
			User:   parts[0],
			Pass:   parts[1],
			Raw:    input,
		}, true, validateHost(parts[2])
	}
	return Proxy{}, true, errors.New("4-part format must contain a valid port in the second or fourth field")
}

func parseHostPort(input, scheme, user, pass, raw string) (Proxy, error) {
	scheme = CanonicalScheme(scheme)
	host, portText, err := net.SplitHostPort(input)
	if err != nil {
		if strings.Count(input, ":") == 1 {
			parts := strings.SplitN(input, ":", 2)
			host = parts[0]
			portText = parts[1]
		} else {
			return Proxy{}, errors.New("host:port must use host:port or [ipv6]:port")
		}
	}
	port, err := parsePort(portText)
	if err != nil {
		return Proxy{}, err
	}
	if err := validateHost(host); err != nil {
		return Proxy{}, err
	}
	if !knownScheme(scheme) {
		return Proxy{}, fmt.Errorf("unsupported scheme %q", scheme)
	}
	return Proxy{
		Scheme: scheme,
		Host:   host,
		Port:   port,
		User:   user,
		Pass:   pass,
		Raw:    raw,
	}, nil
}

func parsePort(text string) (int, error) {
	if !validPort(text) {
		return 0, errors.New("port must be between 1 and 65535")
	}
	port, _ := strconv.Atoi(text)
	return port, nil
}

func validPort(text string) bool {
	if text == "" {
		return false
	}
	port, err := strconv.Atoi(text)
	return err == nil && port >= 1 && port <= 65535
}

func validateHost(host string) error {
	if strings.TrimSpace(host) == "" {
		return errors.New("host is required")
	}
	return nil
}

func knownScheme(scheme string) bool {
	switch CanonicalScheme(scheme) {
	case "socks5", "socks5h", "http", "https":
		return true
	default:
		return false
	}
}
