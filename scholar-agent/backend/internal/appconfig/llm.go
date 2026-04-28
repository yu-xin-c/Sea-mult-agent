package appconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	LLM LLMConfig `toml:"llm"`
}

type LLMConfig struct {
	Model   string `toml:"model"`
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
}

var (
	loadOnce sync.Once
	loadCfg  LLMConfig
	loadErr  error
)

func LoadLLMConfig() (LLMConfig, error) {
	loadOnce.Do(func() {
		cfgPath, err := resolveConfigPath()
		if err != nil {
			loadErr = err
			return
		}

		data, err := os.ReadFile(cfgPath)
		if err != nil {
			loadErr = fmt.Errorf("读取配置文件失败 (%s): %w", cfgPath, err)
			return
		}

		var cfg Config
		if err := toml.Unmarshal(data, &cfg); err != nil {
			loadErr = fmt.Errorf("解析配置文件失败 (%s): %w", cfgPath, err)
			return
		}

		if cfg.LLM.APIKey == "" || cfg.LLM.APIKey == "your-api-key" {
			loadErr = fmt.Errorf("配置文件缺少有效的 llm.api_key（当前值为占位符或空）(%s)", cfgPath)
			return
		}
		if cfg.LLM.BaseURL == "" {
			cfg.LLM.BaseURL = "https://api.deepseek.com/v1"
		}
		if cfg.LLM.Model == "" {
			cfg.LLM.Model = "deepseek-chat"
		}

		loadCfg = cfg.LLM
	})

	if loadErr != nil {
		return LLMConfig{}, loadErr
	}
	return loadCfg, nil
}

func resolveConfigPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	candidates := []string{}
	for i := 0; i < 8; i++ {
		prefix := wd
		for j := 0; j < i; j++ {
			prefix = filepath.Dir(prefix)
		}
		candidates = append(candidates,
			filepath.Join(prefix, "config", "config.toml"),
			filepath.Join(prefix, "config", "config-template.toml"),
			filepath.Join(prefix, "docker-core", "config", "config.toml"),
			filepath.Join(prefix, "docker-core", "config", "config-template.toml"),
		)
	}

	for _, path := range candidates {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path, nil
		}
	}

	return "", fmt.Errorf("未找到 config.toml/config-template.toml（已搜索工作目录及上级目录）")
}
