package server

import (
	"encoding/json"
	"os"
	"time"

	nested "github.com/antonfisher/nested-logrus-formatter"
	log "github.com/sirupsen/logrus"
)

const (
	defaultServerPort     = 8080
	defaultServerEndpoint = "/v1/chat/completions"
)

type account struct {
	Host  string   `json:"host"`
	Token string   `json:"token"`
	Bots  []string `json:"bots"`
}

type ServerConfig struct {
	Port     int    `json:"port"`
	Endpoint string `json:"endpoint"`

	Accounts []*account          `json:"accounts"`
	Models   map[string][]string `json:"models"`
	Tokens   []string            `json:"tokens"`
}

func LoadConfig(cfgPath string) *ServerConfig {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Errorf("read config file error: %v", err)
	}

	cfg := new(ServerConfig)
	if err := json.Unmarshal(raw, cfg); err != nil {
		log.Errorf("parse config file error: %v", err)
	}

	if cfg.Port <= 0 || cfg.Port >= 65535 {
		cfg.Port = defaultServerPort
	}

	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultServerEndpoint
	}

	log.SetFormatter(&nested.Formatter{TimestampFormat: time.DateTime, NoColors: false})

	return cfg
}
