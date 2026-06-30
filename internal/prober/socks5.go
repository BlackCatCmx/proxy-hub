package prober

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"proxy-hub/internal/proxy"
)

type Socks5Prober struct{}

func (Socks5Prober) Probe(ctx context.Context, p proxy.Proxy, target TestTarget) proxy.LatencyResult {
	start := time.Now()
	result := proxy.LatencyResult{
		TestURL:  target.URL,
		TestedAt: time.Now(),
	}
	status, _, dnsMode, err := FetchThroughSOCKS5(ctx, p, target.URL, target.Timeout)
	result.DNSMode = dnsMode
	result.LatencyMs = int(time.Since(start).Milliseconds())
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if target.ExpectedStatus > 0 {
		if status != target.ExpectedStatus {
			result.Error = fmt.Sprintf("unexpected HTTP status %d, expected %d", status, target.ExpectedStatus)
			return result
		}
	} else if status < 200 || status > 299 {
		result.Error = fmt.Sprintf("unexpected HTTP status %d", status)
		return result
	}
	result.OK = true
	return result
}

func FetchThroughSOCKS5(ctx context.Context, p proxy.Proxy, rawURL string, timeout time.Duration) (int, []byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return 0, nil, "", errors.New("only http and https test URLs are supported")
	}
	host := u.Hostname()
	if host == "" {
		return 0, nil, "", errors.New("test URL host is required")
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return 0, nil, "", errors.New("test URL port must be between 1 and 65535")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, dnsMode, err := connectSOCKS5(ctx, p, host, portNum, timeout)
	if err != nil {
		return 0, nil, dnsMode, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return 0, nil, dnsMode, err
	}
	if u.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return 0, nil, dnsMode, err
		}
		conn = tlsConn
	}

	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	hostHeader := u.Host
	if !strings.Contains(hostHeader, ":") && ((u.Scheme == "https" && port != "443") || (u.Scheme == "http" && port != "80")) {
		hostHeader = net.JoinHostPort(hostHeader, port)
	}
	request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: proxy-hub/0.1\r\nAccept: */*\r\nConnection: close\r\n\r\n", path, hostHeader)
	if _, err := io.WriteString(conn, request); err != nil {
		return 0, nil, dnsMode, err
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return 0, nil, dnsMode, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		return 0, nil, dnsMode, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, dnsMode, err
	}
	return resp.StatusCode, body, dnsMode, nil
}

func connectSOCKS5(ctx context.Context, p proxy.Proxy, destHost string, destPort int, timeout time.Duration) (net.Conn, string, error) {
	if ip := net.ParseIP(destHost); ip != nil {
		conn, err := connectSOCKS5Once(ctx, p, socksAddress{ip: ip, port: destPort}, timeout)
		return conn, "ip", err
	}

	switch proxy.CanonicalScheme(p.Scheme) {
	case "socks5":
		return connectSOCKS5Local(ctx, p, destHost, destPort, timeout)
	case "socks5h":
		conn, err := connectSOCKS5Once(ctx, p, socksAddress{domain: destHost, port: destPort}, timeout)
		return conn, "remote", err
	default:
		return nil, "", fmt.Errorf("unsupported proxy scheme %q", p.Scheme)
	}
}

func connectSOCKS5Local(ctx context.Context, p proxy.Proxy, destHost string, destPort int, timeout time.Duration) (net.Conn, string, error) {
	addrs, resolveErr := net.DefaultResolver.LookupIPAddr(ctx, destHost)
	if resolveErr != nil {
		return nil, "local", resolveErr
	}
	var err error
	for _, addr := range addrs {
		conn, localErr := connectSOCKS5Once(ctx, p, socksAddress{ip: addr.IP, port: destPort}, timeout)
		if localErr == nil {
			return conn, "local", nil
		}
		err = localErr
	}
	if err == nil {
		err = errors.New("local DNS returned no addresses")
	}
	return nil, "local", err
}

type socksAddress struct {
	domain string
	ip     net.IP
	port   int
}

func connectSOCKS5Once(ctx context.Context, p proxy.Proxy, dest socksAddress, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(p.Host, strconv.Itoa(p.Port)))
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = conn.Close()
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	if err := socksHandshake(conn, p); err != nil {
		return nil, err
	}
	if err := socksConnect(conn, dest); err != nil {
		return nil, err
	}
	closeOnError = false
	return conn, nil
}

func socksHandshake(conn net.Conn, p proxy.Proxy) error {
	methods := []byte{0x00}
	if p.User != "" || p.Pass != "" {
		methods = []byte{0x00, 0x02}
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[0] != 0x05 {
		return errors.New("invalid SOCKS5 greeting response")
	}
	switch reply[1] {
	case 0x00:
		return nil
	case 0x02:
		return socksPasswordAuth(conn, p.User, p.Pass)
	case 0xff:
		return errors.New("SOCKS5 server rejected all auth methods")
	default:
		return fmt.Errorf("SOCKS5 server selected unsupported auth method 0x%02x", reply[1])
	}
}

func socksPasswordAuth(conn net.Conn, user, pass string) error {
	if len(user) > 255 || len(pass) > 255 {
		return errors.New("SOCKS5 username/password must be at most 255 bytes")
	}
	req := []byte{0x01, byte(len(user))}
	req = append(req, []byte(user)...)
	req = append(req, byte(len(pass)))
	req = append(req, []byte(pass)...)
	if _, err := conn.Write(req); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[0] != 0x01 || reply[1] != 0x00 {
		return errors.New("SOCKS5 username/password auth failed")
	}
	return nil
}

func socksConnect(conn net.Conn, dest socksAddress) error {
	req := []byte{0x05, 0x01, 0x00}
	if dest.domain != "" {
		if len(dest.domain) > 255 {
			return errors.New("SOCKS5 target domain is too long")
		}
		req = append(req, 0x03, byte(len(dest.domain)))
		req = append(req, []byte(dest.domain)...)
	} else {
		ip := dest.ip
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			ip = ip.To16()
			if ip == nil {
				return errors.New("invalid target IP")
			}
			req = append(req, 0x04)
			req = append(req, ip...)
		}
	}
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], uint16(dest.port))
	req = append(req, port[:]...)
	if _, err := conn.Write(req); err != nil {
		return err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return errors.New("invalid SOCKS5 connect response")
	}
	if header[1] != 0x00 {
		return socksReplyError{code: header[1]}
	}
	var skip int
	switch header[3] {
	case 0x01:
		skip = 4
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return err
		}
		skip = int(length[0])
	case 0x04:
		skip = 16
	default:
		return fmt.Errorf("invalid SOCKS5 bind address type 0x%02x", header[3])
	}
	if skip > 0 {
		if _, err := io.CopyN(io.Discard, conn, int64(skip)); err != nil {
			return err
		}
	}
	if _, err := io.CopyN(io.Discard, conn, 2); err != nil {
		return err
	}
	return nil
}

type socksReplyError struct {
	code byte
}

func (e socksReplyError) Error() string {
	return "SOCKS5 connect failed: " + socksReplyMessage(e.code)
}

func socksReplyMessage(code byte) string {
	switch code {
	case 0x01:
		return "general server failure"
	case 0x02:
		return "connection not allowed"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("reply code 0x%02x", code)
	}
}
