package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
)

const configFileName = "config.yaml"

type Config struct {
	ServerID string `yaml:"server-id"`
	Token    string `yaml:"token"`
	ClientID string `yaml:"client-id"`
}

func NewConfig() (*Config, error) {
	configFile, err := os.ReadFile(configFileName)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", configFileName, err)
	}

	var cfg Config
	err = yaml.Unmarshal(configFile, &cfg)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal config file: %w", err)
	}

	return &cfg, nil
}
