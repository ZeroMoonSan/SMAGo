package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type VisionTool struct {
	APIKey   string
	BaseURL  string
	Model    string
	MagickExe string
}

func (v *VisionTool) Definition() ToolDef {
	return ToolDef{
		Name:        "vision",
		Description: "Look at an image file and answer a question about it. Uses mimo-v2.5 multimodal model via OpenCode Go API. Path must be an absolute filesystem path. Supports png/jpg/gif; webp is converted via ImageMagick.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt":     map[string]any{"type": "string", "description": "Question or instruction about the image"},
				"image_path": map[string]any{"type": "string", "description": "Absolute path to the image file"},
			},
			"required": []string{"prompt", "image_path"},
		},
		Execute: v.Execute,
	}
}

func (v *VisionTool) Execute(args map[string]any) (string, error) {
	prompt, _ := args["prompt"].(string)
	imagePath, _ := args["image_path"].(string)
	if prompt == "" || imagePath == "" {
		return "", fmt.Errorf("prompt and image_path required")
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(imagePath), "."))
	if ext == "" {
		ext = "png"
	}

	if _, err := os.Stat(imagePath); err != nil {
		return "", fmt.Errorf("image not found: %w", err)
	}

	// WebP needs conversion because not all providers accept it.
	if ext == "webp" {
		if v.MagickExe == "" {
			v.MagickExe = `C:\Program Files\ImageMagick-7.1.2-Q16-HDRI\magick.exe`
		}
		if _, err := os.Stat(v.MagickExe); err != nil {
			return "", fmt.Errorf("webp conversion needed but magick.exe not found at %s", v.MagickExe)
		}
		tmp := imagePath + ".converted.png"
		cmd := exec.Command(v.MagickExe, imagePath, tmp)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("magick: %v: %s", err, string(out))
		}
		imagePath = tmp
		ext = "png"
	}

	data, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}
	mime := mimeFromExt(ext)
	base64img := base64.StdEncoding.EncodeToString(data)

	body := map[string]any{
		"model": v.Model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "image_url", "image_url": map[string]any{
						"url": fmt.Sprintf("data:%s;base64,%s", mime, base64img),
					}},
					{"type": "text", "text": prompt},
				},
			},
		},
		"max_tokens": 2048,
	}
	bodyJSON, _ := json.Marshal(body)

	httpReq, err := newJSONPost(v.BaseURL+"/chat/completions", bodyJSON, v.APIKey)
	if err != nil {
		return "", err
	}

	resp, err := defaultHTTP.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			respBody = append(respBody, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("decode: %w (body: %s)", err, truncate(string(respBody), 200))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("no choices (body: %s)", truncate(string(respBody), 200))
	}
	return parsed.Choices[0].Message.Content, nil
}

func mimeFromExt(ext string) string {
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	}
	return "image/png"
}
