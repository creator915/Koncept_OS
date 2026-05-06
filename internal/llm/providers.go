package llm

import (
	"fmt"
	"os"
)

// ProviderFromEnv resolves a Config from the KCPOS_PROVIDER env var (default: deepseek).
// Each provider reads its own *_API_KEY env var. All providers covered here use the
// OpenAI-compatible /chat/completions protocol; only baseURL, model, and thinking-mode
// support differ.
func ProviderFromEnv() (Config, error) {
	name := os.Getenv("KCPOS_PROVIDER")
	if name == "" {
		name = "deepseek"
	}
	switch name {
	case "deepseek":
		key := os.Getenv("DEEPSEEK_API_KEY")
		if key == "" {
			return Config{}, fmt.Errorf("DEEPSEEK_API_KEY not set")
		}
		return DefaultDeepSeekConfig(key), nil
	case "moonshot":
		key := os.Getenv("MOONSHOT_API_KEY")
		if key == "" {
			return Config{}, fmt.Errorf("MOONSHOT_API_KEY not set")
		}
		return Config{
			BaseURL:  "https://api.moonshot.cn/v1",
			Model:    envOr("KCPOS_MODEL", "kimi-k2-turbo-preview"),
			APIKey:   key,
			Thinking: false,
		}, nil
	case "qwen":
		key := os.Getenv("DASHSCOPE_API_KEY")
		if key == "" {
			return Config{}, fmt.Errorf("DASHSCOPE_API_KEY not set")
		}
		return Config{
			BaseURL:  "https://dashscope.aliyuncs.com/compatible-mode/v1",
			Model:    envOr("KCPOS_MODEL", "qwen3-max"),
			APIKey:   key,
			Thinking: false,
		}, nil
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return Config{}, fmt.Errorf("OPENAI_API_KEY not set")
		}
		return Config{
			BaseURL:  envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
			Model:    envOr("KCPOS_MODEL", "gpt-5"),
			APIKey:   key,
			Thinking: false,
		}, nil
	default:
		return Config{}, fmt.Errorf("unknown provider %q (supported: deepseek, moonshot, qwen, openai)", name)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
