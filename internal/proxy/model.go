package proxy

import (
	"strings"
	"time"
)

type Group struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Proxies []Proxy   `json:"proxies"`
}

type Proxy struct {
	ID     string `json:"id"`
	Label  string `json:"label,omitempty"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	User   string `json:"user,omitempty"`
	Pass   string `json:"pass,omitempty"`
	Raw    string `json:"raw"`
}

func (p Proxy) Address() string {
	return p.Host + ":" + itoa(p.Port)
}

func (p Proxy) DedupKey() string {
	return CanonicalScheme(p.Scheme) + "\x00" + p.Host + "\x00" + itoa(p.Port) + "\x00" + p.User + "\x00" + p.Pass
}

func CanonicalScheme(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

type ProxyResult struct {
	ProxyID string         `json:"proxy_id"`
	Latency *LatencyResult `json:"latency,omitempty"`
	Echo    *EchoResult    `json:"echo,omitempty"`
}

type LatencyResult struct {
	OK        bool      `json:"ok"`
	LatencyMs int       `json:"latency_ms"`
	TestURL   string    `json:"test_url"`
	DNSMode   string    `json:"dns_mode,omitempty"`
	TestedAt  time.Time `json:"tested_at"`
	Error     string    `json:"error,omitempty"`
}

type EchoResult struct {
	OK              bool      `json:"ok"`
	EchoIP          string    `json:"echo_ip,omitempty"`
	CountryCode     string    `json:"country_code,omitempty"`
	Country         string    `json:"country,omitempty"`
	Region          string    `json:"region,omitempty"`
	City            string    `json:"city,omitempty"`
	Postal          string    `json:"postal,omitempty"`
	ContinentCode   string    `json:"continent_code,omitempty"`
	ASN             int       `json:"asn,omitempty"`
	ASNOrganization string    `json:"asn_organization,omitempty"`
	Organization    string    `json:"organization,omitempty"`
	ISP             string    `json:"isp,omitempty"`
	Latitude        float64   `json:"latitude,omitempty"`
	Longitude       float64   `json:"longitude,omitempty"`
	Timezone        string    `json:"timezone,omitempty"`
	Source          string    `json:"source,omitempty"`
	TestedAt        time.Time `json:"tested_at"`
	Error           string    `json:"error,omitempty"`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
