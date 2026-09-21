package assay

import (
	"encoding/json"
	"strings"
)

func toolResultText(payload []byte) (string, bool) {
	var envelope struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return "", false
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(envelope.Message.Content, &blocks) != nil {
		return "", false
	}
	texts := []string{}
	found := false
	for _, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		found = true
		var text string
		if json.Unmarshal(block.Content, &text) == nil {
			texts = append(texts, text)
			continue
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(block.Content, &parts) == nil {
			for _, part := range parts {
				if part.Type == "text" {
					texts = append(texts, part.Text)
				}
			}
		}
	}
	return strings.Join(texts, "\n"), found
}
