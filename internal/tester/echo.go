package tester

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"proxy-hub/internal/config"
	"proxy-hub/internal/prober"
	"proxy-hub/internal/proxy"
)

type echoSource struct {
	name  string
	url   string
	parse func([]byte) (proxy.EchoResult, error)
}

func (t *Tester) Echo(ctx context.Context, p proxy.Proxy, settings config.Settings) proxy.EchoResult {
	result := proxy.EchoResult{TestedAt: time.Now()}
	switch proxy.CanonicalScheme(p.Scheme) {
	case "socks5", "socks5h", "http", "https":
	default:
		result.Error = "IP echo is not supported for this proxy scheme"
		return result
	}
	timeout := echoSourceTimeout(time.Duration(settings.TimeoutMs) * time.Millisecond)
	var lastErr error
	for _, source := range echoSources() {
		status, body, _, err := prober.FetchThroughProxy(ctx, p, source.url, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		if status < 200 || status > 299 {
			lastErr = errors.New("echo source returned HTTP " + strconv.Itoa(status))
			continue
		}
		parsed, err := source.parse(body)
		if err != nil {
			lastErr = err
			continue
		}
		parsed.OK = true
		parsed.Source = source.name
		parsed.TestedAt = time.Now()
		return parsed
	}
	if lastErr != nil {
		result.Error = lastErr.Error()
	} else {
		result.Error = "all IP echo sources failed"
	}
	return result
}

func echoSourceTimeout(timeout time.Duration) time.Duration {
	if timeout > 3*time.Second {
		return 3 * time.Second
	}
	return timeout
}

func echoSources() []echoSource {
	return []echoSource{
		{name: "ipinfo", url: "https://ipinfo.io/json", parse: parseIPInfo},
		{name: "ip.sb", url: "https://api.ip.sb/geoip", parse: parseIPSB},
		{name: "ipwho.is", url: "https://ipwho.is/", parse: parseIPWhoIs},
	}
}

func parseIPInfo(data []byte) (proxy.EchoResult, error) {
	var payload struct {
		IP       string `json:"ip"`
		Country  string `json:"country"`
		Region   string `json:"region"`
		City     string `json:"city"`
		Postal   string `json:"postal"`
		Timezone string `json:"timezone"`
		Loc      string `json:"loc"`
		Org      string `json:"org"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return proxy.EchoResult{}, err
	}
	if payload.IP == "" {
		return proxy.EchoResult{}, errors.New("ipinfo response missing ip")
	}
	result := proxy.EchoResult{
		EchoIP:      payload.IP,
		CountryCode: payload.Country,
		Region:      payload.Region,
		City:        payload.City,
		Postal:      payload.Postal,
		Timezone:    payload.Timezone,
	}
	if payload.Loc != "" {
		parts := strings.Split(payload.Loc, ",")
		if len(parts) == 2 {
			result.Latitude, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			result.Longitude, _ = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		}
	}
	if strings.HasPrefix(payload.Org, "AS") {
		fields := strings.Fields(payload.Org)
		if len(fields) > 0 {
			result.ASN, _ = strconv.Atoi(strings.TrimPrefix(fields[0], "AS"))
		}
		if len(fields) > 1 {
			org := strings.Join(fields[1:], " ")
			result.ASNOrganization = org
			result.Organization = org
		}
	}
	return result, nil
}

func parseIPSB(data []byte) (proxy.EchoResult, error) {
	var payload struct {
		IP              string  `json:"ip"`
		CountryCode     string  `json:"country_code"`
		Country         string  `json:"country"`
		Organization    string  `json:"organization"`
		ISP             string  `json:"isp"`
		ASN             int     `json:"asn"`
		ASNOrganization string  `json:"asn_organization"`
		Latitude        float64 `json:"latitude"`
		Longitude       float64 `json:"longitude"`
		Timezone        string  `json:"timezone"`
		ContinentCode   string  `json:"continent_code"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return proxy.EchoResult{}, err
	}
	if payload.IP == "" {
		return proxy.EchoResult{}, errors.New("ip.sb response missing ip")
	}
	return proxy.EchoResult{
		EchoIP:          payload.IP,
		CountryCode:     payload.CountryCode,
		Country:         payload.Country,
		Organization:    payload.Organization,
		ISP:             payload.ISP,
		ASN:             payload.ASN,
		ASNOrganization: payload.ASNOrganization,
		Latitude:        payload.Latitude,
		Longitude:       payload.Longitude,
		Timezone:        payload.Timezone,
		ContinentCode:   payload.ContinentCode,
	}, nil
}

func parseIPWhoIs(data []byte) (proxy.EchoResult, error) {
	var payload struct {
		Success       bool    `json:"success"`
		IP            string  `json:"ip"`
		CountryCode   string  `json:"country_code"`
		Country       string  `json:"country"`
		Region        string  `json:"region"`
		City          string  `json:"city"`
		Postal        string  `json:"postal"`
		Latitude      float64 `json:"latitude"`
		Longitude     float64 `json:"longitude"`
		ContinentCode string  `json:"continent_code"`
		Connection    struct {
			ASN int    `json:"asn"`
			Org string `json:"org"`
			ISP string `json:"isp"`
		} `json:"connection"`
		Timezone struct {
			ID string `json:"id"`
		} `json:"timezone"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return proxy.EchoResult{}, err
	}
	if !payload.Success {
		if payload.Message != "" {
			return proxy.EchoResult{}, errors.New(payload.Message)
		}
		return proxy.EchoResult{}, errors.New("ipwho.is returned success=false")
	}
	if payload.IP == "" {
		return proxy.EchoResult{}, errors.New("ipwho.is response missing ip")
	}
	return proxy.EchoResult{
		EchoIP:          payload.IP,
		CountryCode:     payload.CountryCode,
		Country:         payload.Country,
		Region:          payload.Region,
		City:            payload.City,
		Postal:          payload.Postal,
		Latitude:        payload.Latitude,
		Longitude:       payload.Longitude,
		ContinentCode:   payload.ContinentCode,
		ASN:             payload.Connection.ASN,
		Organization:    payload.Connection.Org,
		ASNOrganization: payload.Connection.Org,
		ISP:             payload.Connection.ISP,
		Timezone:        payload.Timezone.ID,
	}, nil
}
