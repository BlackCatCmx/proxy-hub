package store

import (
	"proxy-hub/internal/config"
	"proxy-hub/internal/proxy"
)

type Store interface {
	Groups() ([]proxy.Group, error)
	UpdateGroups(func([]proxy.Group) ([]proxy.Group, error)) error
	SaveGroups([]proxy.Group) error
	Results() (map[string]proxy.ProxyResult, error)
	MergeResults(map[string]proxy.ProxyResult) error
	ClearResults(proxyIDs ...string) error
	Settings() (config.Settings, error)
	SaveSettings(config.Settings) error
}
