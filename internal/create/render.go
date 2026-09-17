package create

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Render produces an editable delivery file from inspected source. Pandoc is a
// document renderer, not an agent runtime. It receives fixed arguments, no shell.
func (w Workflow) Render(ctx context.Context, id, format string) (any, error) {
	if format != "pptx" && format != "html" {
		return nil, fmt.Errorf("render format must be pptx or html")
	}
	o, err := w.DB.RefineryOutput(id)
	if err != nil {
		return nil, err
	}
	if o.Format != "markdown" && o.Format != "marp" {
		return nil, fmt.Errorf("only Markdown and slide source can be rendered")
	}
	if format == "pptx" && o.Kind != "slides" {
		return nil, fmt.Errorf("PowerPoint rendering requires a slides output")
	}
	source, err := w.OwnedFile(o.Path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	binary, err := exec.LookPath("pandoc")
	if err != nil {
		return nil, fmt.Errorf("Pandoc is required to render %s; install Pandoc and retry", format)
	}
	dir := filepath.Join(filepath.Dir(source), "rendered", Digest(raw))
	if err = w.checkWritePath(dir); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	target := filepath.Join(dir, strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))+"."+format)
	if err = w.checkWritePath(target); err != nil {
		return nil, err
	}
	// Strip Marp frontmatter; Pandoc slide boundaries are level-one headings.
	text := string(raw)
	if strings.HasPrefix(strings.TrimSpace(text), "---") {
		lines := strings.Split(text, "\n")
		end := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
		if end >= 0 {
			text = strings.Join(lines[end+1:], "\n")
		}
	}
	text = strings.ReplaceAll(text, "\n---\n", "\n")
	args := []string{"--from=markdown", "--to=" + format, "--standalone", "--output=" + target}
	if format == "pptx" {
		args = append(args, "--slide-level=1")
	} else {
		args = append(args, "--metadata=title:"+o.Title)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("Pandoc render failed: %w: %s", err, out)
	}
	delivery, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	if len(delivery) == 0 {
		return nil, fmt.Errorf("renderer produced an empty file")
	}
	if format == "html" {
		style := `<style>body{max-width:780px;margin:64px auto;padding:0 28px;color:#172b3a;background:#fffdf8;font:19px/1.75 Georgia,serif}h1,h2,h3{font-family:system-ui,sans-serif;line-height:1.2;color:#102c40}h1{font-size:42px}h2{margin-top:2em;font-size:28px}code{font-size:.86em;background:#eef3f6;padding:.12em .3em}p{margin:1.1em 0}header#title-block-header{display:none}hr{border:0;border-top:1px solid #c9d5dc;margin:2em 0}a{color:#175c85}</style>`
		delivery = []byte(strings.Replace(string(delivery), "</head>", style+"</head>", 1))
		if err = os.WriteFile(target, delivery, 0600); err != nil {
			return nil, err
		}
	}
	slides := 0
	if format == "pptx" {
		z, err := zip.OpenReader(target)
		if err != nil {
			return nil, fmt.Errorf("invalid PowerPoint package: %w", err)
		}
		for _, f := range z.File {
			if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
				slides++
			}
		}
		z.Close()
		if slides == 0 {
			return nil, fmt.Errorf("PowerPoint contains no slides")
		}
	}
	manifest := map[string]any{"source_output_id": o.UID, "source_digest": Digest(raw), "source_review_state": o.Status, "rendered_digest": Digest(delivery), "format": format, "renderer": "pandoc", "slides": slides, "provenance_path": o.ProvenancePath, "rendered_at": time.Now().UTC()}
	p, _ := json.MarshalIndent(manifest, "", "  ")
	if err = os.WriteFile(target+".provenance.json", p, 0600); err != nil {
		return nil, err
	}
	return map[string]any{"output_id": id, "format": format, "path": target, "bytes": len(delivery), "slides": slides, "source_digest": Digest(raw), "rendered_digest": Digest(delivery), "provenance_path": target + ".provenance.json", "review_state": o.Status}, nil
}

// SlidePreview exposes the rendered source as a readable local HTML deck for
// inspection when PowerPoint is unavailable. It does not claim pixel parity.
func (w Workflow) SlidePreview(id string) ([]byte, error) {
	o, err := w.DB.RefineryOutput(id)
	if err != nil {
		return nil, err
	}
	if o.Kind != "slides" {
		return nil, fmt.Errorf("output is not a slide deck")
	}
	path, err := w.OwnedFile(o.Path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html><meta charset="utf-8"><title>Slide source preview</title><style>body{margin:0;background:#dce5eb;font:24px/1.5 system-ui;color:#eef5fb}section{box-sizing:border-box;width:960px;min-height:540px;margin:30px auto;padding:55px 65px;background:#102c40;position:relative;border-top:8px solid #41c9aa}h1{font-size:38px;line-height:1.15;margin:0 0 35px}h2{font-size:24px;color:#84d8c8}li{margin:16px 0}small{display:block;color:#91b5c7;font-size:13px;margin-top:35px}</style>`)
	opened := false
	notes := false
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "::: notes") {
			notes = true
			continue
		}
		if notes {
			if strings.TrimSpace(line) == ":::" {
				notes = false
			}
			continue
		}
		if strings.HasPrefix(line, "# ") {
			if opened {
				b.WriteString("</ul></section>")
			}
			n++
			fmt.Fprintf(&b, "<section><small>MIDDEN / RECOVERY LESSONS · %02d</small><h1>%s</h1><ul>", n, html.EscapeString(strings.TrimPrefix(line, "# ")))
			opened = true
		} else if opened && strings.HasPrefix(line, "- ") {
			fmt.Fprintf(&b, "<li>%s</li>", html.EscapeString(strings.TrimPrefix(line, "- ")))
		} else if opened && strings.HasPrefix(line, "## ") {
			fmt.Fprintf(&b, "<h2>%s</h2>", html.EscapeString(strings.TrimPrefix(line, "## ")))
		}
	}
	if opened {
		b.WriteString("</ul></section>")
	}
	b.WriteString("</html>")
	return []byte(b.String()), nil
}

// DeliveryPath only returns an already-rendered file for the current source
// digest, so stale renderings are never downloaded as the current revision.
func (w Workflow) DeliveryPath(id, format string) (string, error) {
	if format != "pptx" && format != "html" {
		return "", fmt.Errorf("unsupported delivery format")
	}
	o, err := w.DB.RefineryOutput(id)
	if err != nil {
		return "", err
	}
	source, err := w.OwnedFile(o.Path)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	target, err := w.OwnedFile(filepath.Join(filepath.Dir(source), "rendered", Digest(raw), strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))+"."+format))
	if err != nil {
		return "", err
	}
	manifestPath, err := w.OwnedFile(target + ".provenance.json")
	if err != nil {
		return "", err
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", err
	}
	var manifest struct {
		SourceDigest   string `json:"source_digest"`
		RenderedDigest string `json:"rendered_digest"`
	}
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", err
	}
	delivery, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	if manifest.SourceDigest != Digest(raw) || manifest.RenderedDigest != Digest(delivery) {
		return "", fmt.Errorf("rendered content or provenance changed; render again")
	}
	return target, nil
}
