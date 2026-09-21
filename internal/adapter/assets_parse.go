package adapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/mekjr1/midden/internal/core"
)

type assetSpec struct {
	name, mediaType, reference, encoded, problem string
	embedded                                     bool
}

type assetObject map[string]json.RawMessage

func (o assetObject) text(key string) string {
	var value string
	json.Unmarshal(o[key], &value)
	return value
}

func (o assetObject) object(key string) assetObject {
	var value assetObject
	json.Unmarshal(o[key], &value)
	return value
}

func parseAssetRecord(tool core.Tool, raw []byte) ([]assetSpec, error) {
	var record assetObject
	if err := json.Unmarshal(raw, &record); err != nil || record == nil {
		return nil, fmt.Errorf("Asset inspection omitted: record is not a supported JSON object.")
	}
	var specs []assetSpec
	addAttachments := func(object assetObject) {
		if len(specs) > MaxAssets {
			return
		}
		if attachments, ok := object["attachments"]; ok {
			err := eachAssetObject(attachments, func(entry assetObject) bool {
				specs = append(specs, parseAssetObject(entry))
				return len(specs) <= MaxAssets
			})
			if err != nil {
				specs = append(specs, assetSpec{problem: "Unsupported logged attachment format."})
			}
		}
	}
	switch tool {
	case core.ToolCopilot:
		data := record.object("data")
		if record.text("type") == "session.binary_asset" {
			specs = append(specs, parseAssetObject(data))
		}
		addAttachments(record)
		addAttachments(data)
	case core.ToolClaude:
		message := record.object("message")
		content := bytes.TrimSpace(message["content"])
		if len(content) > 0 && content[0] == '[' {
			err := eachAssetObject(content, func(block assetObject) bool {
				switch block.text("type") {
				case "image", "document", "file":
					spec := parseAssetObject(block)
					source := block.object("source")
					switch source.text("type") {
					case "base64":
						spec.setBase64(source, "data")
						spec.mediaType = source.text("media_type")
					case "url":
						spec.reference = source.text("url")
					default:
						spec.problem = "Unsupported image or document source format."
					}
					specs = append(specs, spec)
				}
				return len(specs) <= MaxAssets
			})
			if err != nil {
				return nil, fmt.Errorf("Asset inspection omitted: unsupported content block array.")
			}
		}
		addAttachments(record)
		addAttachments(message)
	case core.ToolOpencode:
		if record.text("type") == "file" {
			specs = append(specs, parseAssetObject(record))
		}
		addAttachments(record)
	}
	return specs, nil
}

func eachAssetObject(raw []byte, visit func(assetObject) bool) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return fmt.Errorf("expected an asset array")
	}
	for decoder.More() {
		var entry json.RawMessage
		if err = decoder.Decode(&entry); err != nil {
			return err
		}
		var object assetObject
		if json.Unmarshal(entry, &object) != nil {
			object = nil
		}
		if !visit(object) {
			return nil
		}
	}
	_, err = decoder.Token()
	return err
}

func parseAssetObject(object assetObject) assetSpec {
	spec := assetSpec{
		name:      firstAssetText(object, "name", "filename", "displayName"),
		mediaType: firstAssetText(object, "mimeType", "media_type", "mime", "mediaType"),
		reference: firstAssetText(object, "path", "url", "uri", "dataUrl", "dataURL"),
	}
	if _, ok := object["base64"]; ok {
		spec.setBase64(object, "base64")
	} else if object.text("encoding") == "base64" {
		spec.setBase64(object, "data", "content")
	}
	if !spec.embedded {
		for _, key := range []string{"dataUrl", "dataURL", "url", "uri", "path", "data", "content"} {
			if value := object.text(key); isAssetDataURL(value) {
				spec.reference = value
				break
			}
		}
	}
	return spec
}

func (spec *assetSpec) setBase64(object assetObject, keys ...string) {
	spec.embedded = true
	for _, key := range keys {
		if raw, ok := object[key]; ok {
			var value *string
			if json.Unmarshal(raw, &value) != nil || value == nil {
				spec.problem = "Invalid recorded base64 value; a string is required."
			} else {
				spec.encoded = *value
			}
			return
		}
	}
	spec.problem = "Embedded asset has no recorded base64 data."
}

func firstAssetText(object assetObject, keys ...string) string {
	for _, key := range keys {
		if value := object.text(key); value != "" {
			return value
		}
	}
	return ""
}

func (sc *assetScan) prepare(record int64, ordinal int, spec assetSpec) (RecordedAsset, string) {
	asset := RecordedAsset{RecordIndex: record, Index: ordinal, Status: "unavailable"}
	if len(spec.mediaType) <= 128 {
		if media, _, err := mime.ParseMediaType(spec.mediaType); err == nil {
			asset.MediaType = media
		}
	}
	asset.Name = assetName(spec.name, spec.reference, asset.MediaType)
	if spec.problem != "" {
		return asset, spec.problem
	}
	if isAssetDataURL(spec.reference) && !spec.embedded {
		var err error
		spec.encoded, asset.MediaType, err = assetDataURL(spec.reference, asset.MediaType)
		if err != nil {
			return asset, err.Error()
		}
		spec.embedded = true
		asset.Name = assetName(spec.name, "", asset.MediaType)
	}
	if spec.embedded {
		reader := base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(spec.encoded))
		data, err := io.ReadAll(io.LimitReader(reader, MaxAssetBytes+1))
		if err != nil {
			return asset, "Invalid base64 asset payload."
		}
		if int64(len(data)) > MaxAssetBytes {
			return asset, "Asset omitted: decoded content exceeds 16 MiB."
		}
		sum := sha256.Sum256(data)
		asset.Status = "embedded"
		asset.Digest = "sha256:" + hex.EncodeToString(sum[:])
		asset.Bytes = int64(len(data))
		asset.data = data
		return asset, ""
	}
	if spec.reference == "" {
		return asset, "Asset bytes and a supported local reference were not recorded."
	}
	local, status, reason := assetLocalReference(spec.reference)
	if reason != "" {
		asset.Status = status
		return asset, reason
	}
	location, size, status, reason := locateAsset(local, sc.roots)
	asset.location, asset.Bytes, asset.Status = location, size, status
	return asset, reason
}

func assetDataURL(raw, media string) (string, string, error) {
	header, data, ok := strings.Cut(raw[len("data:"):], ",")
	if !ok || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return "", "", fmt.Errorf("Unsupported data URI: only explicit base64 encoding is supported.")
	}
	declared := header[:len(header)-len(";base64")]
	if declared != "" {
		parsed, _, err := mime.ParseMediaType(declared)
		if err != nil || len(parsed) > 128 {
			return "", "", fmt.Errorf("Invalid data URI media type.")
		}
		media = parsed
	}
	decoded, err := url.PathUnescape(data)
	if err != nil {
		return "", "", fmt.Errorf("Invalid base64 data URI escaping.")
	}
	return decoded, media, nil
}

func isAssetDataURL(value string) bool {
	return len(value) >= len("data:") && strings.EqualFold(value[:len("data:")], "data:")
}

func assetName(name, reference, media string) string {
	if name == "" && !isAssetDataURL(reference) {
		name = reference
		if !filepath.IsAbs(reference) {
			if uri, err := url.Parse(reference); err == nil && uri.Scheme != "" {
				name = uri.Path
			}
		}
	}
	if strings.Contains(strings.ToLower(name), "base64") || strings.HasPrefix(strings.ToLower(name), "data:") {
		name = ""
	}
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	var safe strings.Builder
	for _, char := range name {
		if safe.Len() >= 96 {
			break
		}
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '-' || char == '_' {
			safe.WriteByte(byte(char))
		} else {
			safe.WriteByte('_')
		}
	}
	name = strings.Trim(safe.String(), " .")
	if name == "" {
		extension := map[string]string{
			"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
			"image/webp": ".webp", "image/svg+xml": ".svg", "application/pdf": ".pdf",
			"text/plain": ".txt",
		}[media]
		if extension == "" {
			extension = ".bin"
		}
		name = "asset" + extension
	}
	return name
}
