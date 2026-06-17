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
	Name          string `json:"name"`
	ContextWindow int    `json:"contextWindow,omitempty"`
}

type DCPConfig struct {
	Enabled            bool `json:"dcpEnabled"`
	MaxContextTokens   int  `json:"dcpMaxContextTokens"`
	MinContextTokens   int  `json:"dcpMinContextTokens"`
	NudgeFrequency     int  `json:"dcpNudgeFrequency"`
	PurgeErrorsTurns   int  `json:"dcpPurgeErrorsTurns"`
	ProtectRecentCount int  `json:"dcpProtectRecentCount"`
	ShowNotifications  bool `json:"dcpShowNotifications"`
	ManualMode         bool `json:"dcpManualMode"`
}

type Config struct {
	TelegramToken  string                    `json:"telegramToken"`
	TelegramChatID int64                     `json:"telegramChatID"`
	DefaultModel   string                    `json:"defaultModel"`
	Provider       string                    `json:"provider"`
	Providers      map[string]Provider       `json:"providers"`
	MCP            map[string]MCPServerConfig `json:"mcp"`
	SystemPrompt   string                    `json:"systemPrompt"`
	DataDir        string                    `json:"dataDir"`
	TrustedChatIDs []int64                   `json:"trustedChatIDs"`
	MagickExe      string                    `json:"magickExe"`
	DefaultShell   string                    `json:"defaultShell"`
	DCP            DCPConfig                 `json:"dcp"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		DataDir:      "./data",
		SystemPrompt: "You are a helpful AI assistant. Be concise. Use tools when helpful.",
		DefaultShell: "powershell",
		DCP: DCPConfig{
			Enabled:            true,
			MaxContextTokens:   80000,
			MinContextTokens:   40000,
			NudgeFrequency:     5,
			PurgeErrorsTurns:   4,
			ProtectRecentCount: 4,
		},
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
