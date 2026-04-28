package config

import (
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	LLM      LLMConfig      `toml:"llm"`
	Resource ResourceConfig `toml:"resource"`
	Watchdog WatchdogConfig `toml:"watchdog"`
}

type LLMConfig struct {
	Model   string `toml:"model"`
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
}

type ResourceConfig struct {
	MaxConcurrentIO int64 `toml:"max_concurrent_io"`
	MaxGPUTasks     int64 `toml:"max_gpu_tasks"`
}

type WatchdogConfig struct {
	MaxRetryWindow int   `toml:"max_retry_window"`
	CommandTimeout int64 `toml:"command_timeout_seconds"`
}

func LoadConfig(path string) (*Config, error) {
	var config Config
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return nil, err
	}
	// Override API key from environment if exists
	if apiKey := os.Getenv("LLM_API_KEY"); apiKey != "" {
		config.LLM.APIKey = apiKey
	}
	return &config, nil
}
