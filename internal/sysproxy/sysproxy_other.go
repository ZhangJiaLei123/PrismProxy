//go:build !windows

package sysproxy

import "errors"

// Config 系统代理配置（非 Windows 平台仅占位）
type Config struct {
	Enable        bool   `json:"enable"`
	Server        string `json:"server"`
	Override      string `json:"override"`
	AutoConfigURL string `json:"autoConfigURL"`

	ServerExists   bool `json:"serverExists"`
	OverrideExists bool `json:"overrideExists"`
	AutoURLExists  bool `json:"autoURLExists"`
}

const (
	StateOff      = "off"
	StateOn       = "on"
	StateOccupied = "occupied"
)

var errUnsupported = errors.New("sysproxy: 仅支持 Windows")

func Current() (Config, error)              { return Config{}, errUnsupported }
func Status(string) (string, Config, error) { return StateOff, Config{}, errUnsupported }
func Enable(string, []string, string) error { return errUnsupported }
func Reapply(string, []string, string) error { return errUnsupported }
func Disable(string, string) error          { return errUnsupported }
func SelfHeal(string) (bool, error)         { return false, nil }
func UpstreamFromSystem(string) string      { return "" }
