package appconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		cfg := LLMConfig{}

		cfgPath, err := resolveConfigPath()
		if err == nil {
			data, readErr := os.ReadFile(cfgPath)
			if readErr != nil {
				loadErr = fmt.Errorf("读取配置文件失败 (%s): %w", cfgPath, readErr)
				return
			}

			var fileCfg Config
			if unmarshalErr := toml.Unmarshal(data, &fileCfg); unmarshalErr != nil {
				loadErr = fmt.Errorf("解析配置文件失败 (%s): %w", cfgPath, unmarshalErr)
				return
			}
			cfg = fileCfg.LLM
		}

		// 兼容旧版环境变量配置，并允许环境变量覆盖文件配置。
		// 对 API Key 会跳过占位符值，避免无效 OPENAI_API_KEY 抢占真实 LLM_API_KEY。
		if apiKey := strings.TrimSpace(firstValidAPIKeyFromEnv("OPENAI_API_KEY", "LLM_API_KEY")); apiKey != "" {
			cfg.APIKey = apiKey
		}
		if baseURL := strings.TrimSpace(firstNonEmptyEnv("OPENAI_BASE_URL", "LLM_BASE_URL")); baseURL != "" {
			cfg.BaseURL = baseURL
		}
		if model := strings.TrimSpace(firstNonEmptyEnv("OPENAI_MODEL_NAME", "LLM_MODEL_NAME")); model != "" {
			cfg.Model = model
		}

		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.deepseek.com/v1"
		}
		if cfg.Model == "" {
			cfg.Model = "deepseek-chat"
		}

		if isPlaceholderAPIKey(cfg.APIKey) {
			loadErr = fmt.Errorf("未检测到有效 llm.api_key：请在 config.toml 中配置真实 Key，或设置 OPENAI_API_KEY/LLM_API_KEY")
			return
		}

		loadCfg = cfg
	})

	if loadErr != nil {
		return LLMConfig{}, loadErr
	}
	return loadCfg, nil
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstValidAPIKeyFromEnv(keys ...string) string {
	for _, k := range keys {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			continue
		}
		if isPlaceholderAPIKey(v) {
			continue
		}
		return v
	}
	return ""
}

func isPlaceholderAPIKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return true
	}
	placeholders := []string{
		"your-api-key",
		"your_api_key",
		"sk-your-key",
		"replace-me",
		"changeme",
		"api-key-here",
		"your_openai_api_key",
	}
	for _, p := range placeholders {
		if k == p {
			return true
		}
	}
	return false
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
