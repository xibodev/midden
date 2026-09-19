package module

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mekjr1/midden/internal/index"
)

const AgentReplyTarget = 8 * 1024
const AgentReplyCeiling = 16 * 1024

type ResultInspectInput struct {
	ResultID string `json:"result_id"`
	Path     string `json:"path,omitempty"`
	Offset   int    `json:"offset,omitempty" min:"0"`
	Limit    int    `json:"limit,omitempty" min:"1" max:"20" default:"5"`
}
type ResultField struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Count int    `json:"count,omitempty"`
}
type ResultPage struct {
	ResultID   string        `json:"result_id"`
	Path       string        `json:"path"`
	Kind       string        `json:"kind"`
	Offset     int           `json:"offset"`
	Total      int           `json:"total"`
	Value      any           `json:"value"`
	Complete   bool          `json:"complete"`
	NextOffset *int          `json:"next_offset,omitempty"`
	Fields     []ResultField `json:"fields,omitempty"`
}

// InvokeAgent is the compact delivery surface. The underlying module contract
// remains available to native consumers; source stores are never used as caches.
func InvokeAgent(req Request) Envelope {
	env := Invoke(req)
	if req.Capability == "results.inspect" {
		return boundedEnvelope(req, env)
	}
	if !env.OK {
		return boundedEnvelope(req, env)
	}
	mutation := compactMutation(req.Capability)
	if !mutation && envelopeBytes(env) <= AgentReplyTarget {
		return env
	}
	root, ok := req.Roots[RootMiddenHome]
	if !ok || root.Mode != "rw" || !filepath.IsAbs(root.Path) {
		return invalidRequest(req, fmt.Errorf("compact result delivery requires a writable Midden state root"))
	}
	db, err := index.OpenAt(root.Path)
	if err != nil {
		return invalidRequest(req, err)
	}
	defer db.Close()
	id := "view-" + strings.TrimPrefix(DigestSHA256(env.Result), DigestPrefix)
	if err = db.PutAgentView(id, req.Capability, env.Result); err != nil {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{Code: "result_view_failed", Message: err.Error(), Details: map[string]any{"operation_completed": true}}, LocalFree())
	}
	value, err := decodeView(env.Result)
	if err != nil {
		return invalidRequest(req, err)
	}
	limit := 12
	if mutation {
		limit = 3
	}
	for ; limit >= 1; limit /= 2 {
		var preview any
		if mutation {
			preview = mutationSummary(value)
		} else {
			preview = previewValue(value, 0, limit)
		}
		object, ok := preview.(map[string]any)
		if !ok {
			object = map[string]any{"value": preview}
		}
		object["_view"] = map[string]any{"result_id": id, "preview": true, "inspect": "results.inspect", "fields": viewFields(value, "")}
		env.Result, _ = json.Marshal(object)
		target := AgentReplyTarget
		if mutation {
			target = 2048
		}
		if envelopeBytes(env) <= target {
			return env
		}
		if mutation {
			break
		}
	}
	env.Result, _ = json.Marshal(map[string]any{"_view": map[string]any{"result_id": id, "preview": true, "inspect": "results.inspect", "fields": viewFields(value, "")}})
	return boundedEnvelope(req, env)
}

func compactMutation(cap string) bool {
	switch cap {
	case "projects.create", "projects.update", "editorial.analyze", "editorial.select", "recipes.design", "recipes.update", "recipes.evidence", "recipes.compose", "recipes.produce", "outputs.review", "outputs.export", "outputs.render", "evidence.compose", "handoffs.create":
		return true
	}
	return false
}

func mutationSummary(value any) map[string]any {
	out := map[string]any{}
	object, ok := value.(map[string]any)
	if !ok {
		return out
	}
	for _, key := range []string{"id", "uid", "title", "revision", "status", "packet_id", "stored", "review_state", "content_digest", "path", "provenance_path", "manifest_path", "manifest_digest", "root", "target", "destination"} {
		if v, ok := object[key]; ok {
			out[key] = shortValue(key, v)
		}
	}
	for _, key := range []string{"project", "recipe", "output", "run"} {
		if v, ok := object[key]; ok {
			out[key] = mutationSummary(v)
		}
	}
	for _, key := range []string{"evidence", "outputs"} {
		if rows, ok := object[key].([]any); ok {
			out[key+"_count"] = len(rows)
			items := []any{}
			for i, v := range rows {
				if i == 3 {
					break
				}
				items = append(items, mutationSummary(v))
			}
			out[key] = items
		}
	}
	return out
}

func previewValue(value any, depth, limit int) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		keys := orderedKeys(v)
		for i, key := range keys {
			if i >= 16 {
				break
			}
			if key == "request" || key == "prompt" || key == "body" || key == "provenance" {
				continue
			}
			if depth >= 2 {
				switch child := v[key].(type) {
				case []any:
					out[key+"_count"] = len(child)
					continue
				case map[string]any:
					out[key+"_fields"] = len(child)
					continue
				}
			}
			if str, ok := v[key].(string); ok {
				out[key] = shortValue(key, str)
				if key == "excerpt" && out[key] != str {
					out["preview_only"] = true
				}
			} else {
				out[key] = previewValue(v[key], depth+1, limit)
			}
		}
		return out
	case []any:
		out := []any{}
		n := len(v)
		if n > limit {
			n = limit
		}
		for i := 0; i < n; i++ {
			index := i
			if n > 1 && len(v) > n {
				index = i * (len(v) - 1) / (n - 1)
			}
			out = append(out, previewValue(v[index], depth+1, limit))
		}
		return out
	case string:
		return shortValue("", v)
	default:
		return v
	}
}

func shortValue(key string, value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	limit := 180
	if key == "id" || key == "uid" || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_digest") || key == "path" || strings.HasSuffix(key, "_path") {
		limit = 1024
	}
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + " [preview]"
	}
	return text
}

func orderedKeys(object map[string]any) []string {
	priority := []string{"id", "uid", "packet_id", "investigation_id", "revision", "status", "title", "source", "source_first_time", "source_last_time", "budget", "selection", "records", "analysis", "opportunities"}
	out := []string{}
	seen := map[string]bool{}
	for _, key := range priority {
		if _, ok := object[key]; ok {
			out = append(out, key)
			seen[key] = true
		}
	}
	rest := []string{}
	for key := range object {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func viewFields(value any, path string) []ResultField {
	fields := []ResultField{}
	object, ok := value.(map[string]any)
	if !ok {
		return fields
	}
	for _, key := range orderedKeys(object) {
		if len(fields) >= 32 {
			break
		}
		kind, count := viewKind(object[key])
		fields = append(fields, ResultField{path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"), kind, count})
	}
	return fields
}

func viewKind(value any) (string, int) {
	switch v := value.(type) {
	case map[string]any:
		return "object", len(v)
	case []any:
		return "array", len(v)
	case string:
		return "text", len([]rune(v))
	default:
		return "scalar", 1
	}
}

func decodeView(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	err := d.Decode(&value)
	return value, err
}

func inspectResult(req Request, db *index.DB, input ResultInspectInput) (ResultPage, error) {
	var page ResultPage
	if input.Offset < 0 || input.Limit < 0 || input.Limit > 20 || len(input.Path) > 1024 {
		return page, fmt.Errorf("invalid result page bounds")
	}
	if input.Limit == 0 {
		input.Limit = 5
	}
	raw, err := db.AgentView(input.ResultID)
	if err != nil {
		return page, fmt.Errorf("result view unavailable or expired; re-inspect the durable object: %w", err)
	}
	value, err := decodeView(raw)
	if err != nil {
		return page, err
	}
	if input.Path != "" {
		if !strings.HasPrefix(input.Path, "/") {
			return page, fmt.Errorf("path must be a returned JSON pointer beginning with /")
		}
		for _, token := range strings.Split(input.Path[1:], "/") {
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			switch v := value.(type) {
			case map[string]any:
				var ok bool
				value, ok = v[token]
				if !ok {
					return page, fmt.Errorf("unknown result field %q", token)
				}
			case []any:
				i, e := strconv.Atoi(token)
				if e != nil || i < 0 || i >= len(v) {
					return page, fmt.Errorf("invalid result array index")
				}
				value = v[i]
			default:
				return page, fmt.Errorf("result path enters a scalar")
			}
		}
	}
	kind, total := viewKind(value)
	if input.Offset > total {
		return page, fmt.Errorf("offset is beyond the result")
	}
	page = ResultPage{ResultID: input.ResultID, Path: input.Path, Kind: kind, Offset: input.Offset, Total: total, Complete: true}
	switch v := value.(type) {
	case string:
		runes := []rune(v)
		end := input.Offset + 1024
		if end > len(runes) {
			end = len(runes)
		}
		page.Value = string(runes[input.Offset:end])
		if end < len(runes) {
			page.NextOffset = &end
			page.Complete = false
		}
	case []any:
		page.Value = []any{}
		end := input.Offset + input.Limit
		if end > len(v) {
			end = len(v)
		}
		for end > input.Offset {
			page.Value = v[input.Offset:end]
			wire, _ := json.Marshal(page)
			if len(wire) < AgentReplyTarget-1024 {
				break
			}
			end--
		}
		if end == input.Offset && input.Offset < len(v) {
			end = input.Offset + 1
			page.Value = []any{previewValue(v[input.Offset], 0, 2)}
			itemKind, itemCount := viewKind(v[input.Offset])
			page.Fields = []ResultField{{fmt.Sprintf("%s/%d", input.Path, input.Offset), itemKind, itemCount}}
			page.Complete = false
		}
		if end < len(v) {
			page.NextOffset = &end
			page.Complete = false
		}
	case map[string]any:
		keys := orderedKeys(v)
		end := input.Offset + input.Limit
		if end > len(keys) {
			end = len(keys)
		}
		for {
			selected := map[string]any{}
			page.Fields = []ResultField{}
			page.Complete = true
			for _, key := range keys[input.Offset:end] {
				child := v[key]
				raw, _ := json.Marshal(child)
				if len(raw) > 1024 {
					child = previewValue(child, 0, 2)
					page.Complete = false
				}
				selected[key] = child
				kind, count := viewKind(v[key])
				path := input.Path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
				page.Fields = append(page.Fields, ResultField{path, kind, count})
			}
			page.Value = selected
			raw, _ := json.Marshal(page)
			if len(raw) <= AgentReplyTarget-1024 || end <= input.Offset+1 {
				break
			}
			end--
		}
		if end < len(keys) {
			page.NextOffset = &end
			page.Complete = false
		}
	default:
		page.Value = value
	}
	return page, nil
}

func envelopeBytes(env Envelope) int { raw, _ := json.Marshal(env); return len(raw) }

func boundedEnvelope(req Request, env Envelope) Envelope {
	if envelopeBytes(env) <= AgentReplyCeiling {
		return env
	}
	if env.Error != nil {
		env.Error.Message = fmt.Sprint(shortValue("", env.Error.Message))
		env.Error.Details = map[string]any{"details_omitted": true, "next_action": "narrow the request or inspect the referenced object"}
		env.Warnings = nil
		if envelopeBytes(env) <= AgentReplyCeiling {
			return env
		}
	}
	return NewErrorEnvelope(OpInvoke, "", Error{Code: "response_page_too_large", Message: "Result exceeds the inline response ceiling; inspect a narrower field/page."}, LocalFree())
}
