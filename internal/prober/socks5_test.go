package prober

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"proxy-hub/internal/proxy"
)

type capturedSOCKSRequest struct {
	atyp   byte
	domain string
	port   int
}

func TestConnectSOCKS5UsesLocalDNSForSocks5(t *testing.T) {
	server := startSOCKS5CaptureServer(t)
	p := proxy.Proxy{Scheme: "socks5", Host: server.host, Port: server.port}

	conn, dnsMode, err := connectSOCKS5(context.Background(), p, "localhost", 443, time.Second)
	if err != nil {
		t.Fatalf("connectSOCKS5() error = %v", err)
	}
	conn.Close()
	if dnsMode != "local" {
		t.Fatalf("dnsMode = %q, want local", dnsMode)
	}

	req := server.request(t)
	if req.atyp == 0x03 {
		t.Fatalf("socks5 sent domain to proxy instead of locally resolved IP")
	}
	if req.port != 443 {
		t.Fatalf("port = %d, want 443", req.port)
	}
}

func TestConnectSOCKS5UsesRemoteDNSForSocks5H(t *testing.T) {
	server := startSOCKS5CaptureServer(t)
	p := proxy.Proxy{Scheme: "socks5h", Host: server.host, Port: server.port}

	conn, dnsMode, err := connectSOCKS5(context.Background(), p, "example.test", 443, time.Second)
	if err != nil {
		t.Fatalf("connectSOCKS5() error = %v", err)
	}
	conn.Close()
	if dnsMode != "remote" {
		t.Fatalf("dnsMode = %q, want remote", dnsMode)
	}

	req := server.request(t)
	if req.atyp != 0x03 {
		t.Fatalf("socks5h address type = 0x%02x, want domain type 0x03", req.atyp)
	}
	if req.domain != "example.test" {
		t.Fatalf("domain = %q, want example.test", req.domain)
	}
	if req.port != 443 {
		t.Fatalf("port = %d, want 443", req.port)
	}
}

func TestConnectSOCKS5HDoesNotFallbackToLocalDNS(t *testing.T) {
	server := startSOCKS5CaptureServerWithReply(t, 0x08)
	p := proxy.Proxy{Scheme: "socks5h", Host: server.host, Port: server.port}

	conn, dnsMode, err := connectSOCKS5(context.Background(), p, "localhost", 443, time.Second)
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("connectSOCKS5() error = nil, want SOCKS5 reply error")
	}
	if dnsMode != "remote" {
		t.Fatalf("dnsMode = %q, want remote", dnsMode)
	}

	var replyErr socksReplyError
	if !errors.As(err, &replyErr) || replyErr.code != 0x08 {
		t.Fatalf("error = %v, want SOCKS5 reply code 0x08", err)
	}
	req := server.request(t)
	if req.atyp != 0x03 {
		t.Fatalf("socks5h address type = 0x%02x, want domain type 0x03", req.atyp)
	}
	if req.domain != "localhost" {
		t.Fatalf("domain = %q, want localhost", req.domain)
	}
}

type socks5CaptureServer struct {
	host     string
	port     int
	reply    byte
	requestC chan capturedSOCKSRequest
	errC     chan error
}

func startSOCKS5CaptureServer(t *testing.T) socks5CaptureServer {
	t.Helper()
	return startSOCKS5CaptureServerWithReply(t, 0x00)
}

func startSOCKS5CaptureServerWithReply(t *testing.T, reply byte) socks5CaptureServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() {
		listener.Close()
	})

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi() error = %v", err)
	}

	server := socks5CaptureServer{
		host:     host,
		port:     port,
		reply:    reply,
		requestC: make(chan capturedSOCKSRequest, 1),
		errC:     make(chan error, 1),
	}
	go server.serve(listener)
	return server
}

func (s socks5CaptureServer) serve(listener net.Listener) {
	defer listener.Close()

	conn, err := listener.Accept()
	if err != nil {
		s.errC <- err
		return
	}
	defer conn.Close()

	req, err := readSOCKS5Request(conn)
	if err != nil {
		s.errC <- err
		return
	}
	s.requestC <- req
	if s.reply == 0x00 {
		_, err = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	} else {
		_, err = conn.Write([]byte{0x05, s.reply, 0x00, 0x01})
	}
	if err != nil {
		s.errC <- err
		return
	}
	if s.reply != 0x00 {
		return
	}
	_, _ = io.Copy(io.Discard, conn)
}

func (s socks5CaptureServer) request(t *testing.T) capturedSOCKSRequest {
	t.Helper()

	select {
	case req := <-s.requestC:
		return req
	case err := <-s.errC:
		t.Fatalf("SOCKS5 capture server error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SOCKS5 request")
	}
	return capturedSOCKSRequest{}
}

func readSOCKS5Request(conn net.Conn) (capturedSOCKSRequest, error) {
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return capturedSOCKSRequest{}, err
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return capturedSOCKSRequest{}, err
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return capturedSOCKSRequest{}, err
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return capturedSOCKSRequest{}, err
	}
	req := capturedSOCKSRequest{atyp: header[3]}
	switch header[3] {
	case 0x01:
		if _, err := io.CopyN(io.Discard, conn, 4); err != nil {
			return capturedSOCKSRequest{}, err
		}
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return capturedSOCKSRequest{}, err
		}
		domain := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, domain); err != nil {
			return capturedSOCKSRequest{}, err
		}
		req.domain = string(domain)
	case 0x04:
		if _, err := io.CopyN(io.Discard, conn, 16); err != nil {
			return capturedSOCKSRequest{}, err
		}
	}

	var port [2]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return capturedSOCKSRequest{}, err
	}
	req.port = int(binary.BigEndian.Uint16(port[:]))
	return req, nil
}
