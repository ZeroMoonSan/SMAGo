package main

import (
	"encoding/json"
	"os"
)

type Provider struct {
	Name    string            `json:"name"`
	BaseURL string            `json:"baseURL"`
	APIKey  string            `json:"apiKey"`
	Models  map[string]Model  `json:"models"`
	Headers map[string]string `json:"headers"`
}

type Model struct {
	Name string `json:"name"`
}

type Config struct {
	TelegramToken  string              `json:"telegramToken"`
	TelegramChatID int64               `json:"telegramChatID"`
	DefaultModel   string              `json:"defaultModel"`
	Provider       string              `json:"provider"`
	Providers      map[string]Provider `json:"providers"`
	SystemPrompt   string              `json:"systemPrompt"`
	DataDir        string              `json:"dataDir"`
	TrustedChatIDs []int64             `json:"trustedChatIDs"`
	MagickExe      string              `json:"magickExe"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		DataDir:      "./data",
		SystemPrompt: "You are a helpful AI assistant. Be concise. Use tools when helpful.",
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
