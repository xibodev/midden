package module

import (
	"bytes"
	"encoding/json"
)

// The agent surface reports observable review findings, not the legacy UI's
// weighted quality number. A model-provided confidence is not factual accuracy.
func workflowWireResult(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var object any
	if err = d.Decode(&object); err != nil {
		return nil, err
	}
	stripWorkflowScores(object, false)
	return json.Marshal(object)
}

func stripWorkflowScores(value any, schema bool) {
	switch v := value.(type) {
	case map[string]any:
		if schema {
			if props, ok := v["properties"].(map[string]any); ok {
				delete(props, "quality")
				delete(props, "coverage")
				if confidence, ok := props["mean_confidence"]; ok {
					delete(props, "mean_confidence")
					props["model_supplied_confidence_mean"] = confidence
					delete(props, "needs_review")
					props["automatic_fact_checking"] = map[string]any{"type": "string", "enum": []string{"not_performed"}}
				}
				if fields, ok := v["required"].([]any); ok {
					out := []any{}
					for _, field := range fields {
						switch field {
						case "quality", "coverage", "needs_review":
							continue
						case "mean_confidence":
							out = append(out, "model_supplied_confidence_mean", "automatic_fact_checking")
						default:
							out = append(out, field)
						}
					}
					v["required"] = out
				}
			}
		} else {
			delete(v, "quality")
			delete(v, "coverage")
			if confidence, ok := v["mean_confidence"]; ok {
				delete(v, "mean_confidence")
				delete(v, "needs_review")
				v["model_supplied_confidence_mean"] = confidence
				v["automatic_fact_checking"] = "not_performed"
			}
		}
		for _, child := range v {
			stripWorkflowScores(child, schema)
		}
	case []any:
		for _, child := range v {
			stripWorkflowScores(child, schema)
		}
	}
}
