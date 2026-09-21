package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/material"
)

type repeatedStrings []string

func (s *repeatedStrings) String() string { return strings.Join(*s, ",") }
func (s *repeatedStrings) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		if strings.TrimSpace(part) != "" {
			*s = append(*s, strings.TrimSpace(part))
		}
	}
	return nil
}

type materialPage struct {
	material.View
	Offset          int  `json:"offset"`
	SelectedRecords int  `json:"selected_records"`
	NextOffset      *int `json:"next_offset,omitempty"`
}

func materialFlags(name string) (*flag.FlagSet, *material.Service, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	service := &material.Service{State: index.Dir(), Roots: adapter.EnvironmentRoots()}
	fs.StringVar(&service.State, "state", index.Dir(), "Midden cache directory (not a source store)")
	fs.StringVar(&service.Roots.Copilot, "copilot-root", service.Roots.Copilot, "explicit Copilot source store")
	fs.StringVar(&service.Roots.Claude, "claude-root", service.Roots.Claude, "explicit Claude source store")
	fs.StringVar(&service.Roots.Opencode, "opencode-db", service.Roots.Opencode, "explicit OpenCode source database")
	fs.BoolVar(&service.Roots.Strict, "sources-only", service.Roots.Strict, "use only explicitly supplied source stores")
	asJSON := fs.Bool("json", false, "structured result without a module envelope")
	return fs, service, asJSON
}

func runMaterial(command string, args []string, out io.Writer) error {
	if command == "collection" {
		return runCollection(args, out)
	}
	fs, service, asJSON := materialFlags(command)
	var views, records repeatedStrings
	fs.Var(&views, "view", "source view id (repeat for collection)")
	fs.Var(&records, "record", "source record/window id (repeat to select)")
	tool := fs.String("tool", "", "source tool: copilot, claude or opencode")
	session := fs.String("session", "", "exact source session id")
	limit := fs.Int("limit", 24, "maximum records to sample/read (1..256)")
	chars := fs.Int("chars", 800, "maximum visible characters per source window (80..8192)")
	before := fs.Int("before", 0, "records of context before each selection (0..5)")
	after := fs.Int("after", 0, "records of context after each selection (0..5)")
	offset := fs.Int("offset", 0, "record page offset")
	includeTools := fs.Bool("include-tools", false, "include recorded tool output as evidence")
	includeAssets := fs.Bool("assets", false, "copy available assets belonging to explicitly selected collection records")
	destination := fs.String("out", "", "new collection directory or full read-result JSON file")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	state, err := filepath.Abs(service.State)
	if err != nil {
		return err
	}
	service.State = state
	opts := material.ReadOptions{Records: records, Limit: *limit, Chars: *chars, Before: *before, After: *after, IncludeTools: *includeTools}
	switch command {
	case "read", "search":
		var view material.View
		if command == "search" {
			if len(views) != 1 || fs.NArg() != 1 {
				return fmt.Errorf("usage: midden search <literal phrase> --view <id> [--json]")
			}
			view, err = service.Search(views[0], fs.Arg(0), opts)
		} else if len(views) == 1 {
			if fs.NArg() != 0 || *session != "" || *tool != "" {
				return fmt.Errorf("read a view or specify an exact source, not both")
			}
			// Pagination is applied after source retrieval so the cached view
			// remains complete and its identity does not depend on display size.
			readOpts := opts
			if len(records) == 0 {
				readOpts.Limit = 0
			}
			view, err = service.Read(views[0], readOpts)
		} else {
			if len(views) > 1 || fs.NArg() != 0 {
				return fmt.Errorf("usage: midden read --tool <tool> --session <exact-id>")
			}
			if len(records) > 0 || *before > 0 || *after > 0 {
				return fmt.Errorf("focused --record/--before/--after reads require --view; open the exact source first")
			}
			view, err = service.Open(material.Source{Tool: core.Tool(*tool), ID: *session}, opts)
		}
		if err != nil {
			return err
		}
		if *destination != "" {
			if err := service.CheckDestination(*destination); err != nil {
				return err
			}
			raw, err := json.MarshalIndent(view, "", "  ")
			if err != nil {
				return err
			}
			if err = writeCLIFile(*destination, append(raw, '\n')); err != nil {
				return err
			}
			return emitMaterial(out, *asJSON, map[string]any{"path": *destination, "view_id": view.ID, "records": len(view.Records)})
		}
		page, err := pageView(view, *offset, *limit)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(page)
		}
		fmt.Fprintf(out, "View: %s\nSource: %s:%s\nRange: %s -> %s\nSelection: %s; %d of %d retained records shown\n\n", page.ID, page.Source.Tool, page.Source.ID, page.FirstTime.Format("2006-01-02"), page.LastTime.Format("2006-01-02"), page.Selection, len(page.Records), page.SelectedRecords)
		for _, r := range page.Records {
			fmt.Fprintf(out, "[%s] %s %s/%s (clipped=%t)\n%s\n\n", r.ID, r.Time.Format("2006-01-02T15:04:05Z07:00"), r.Kind, r.Role, r.Clipped, r.Text)
		}
		if page.NextOffset != nil {
			fmt.Fprintf(out, "More retained records: read --view %s --offset %d\n", page.ID, *page.NextOffset)
		}
		for _, warning := range page.Warnings {
			fmt.Fprintf(out, "Note: %s\n", warning)
		}
		return nil
	case "collect":
		if fs.NArg() != 0 {
			return fmt.Errorf("collect accepts --view, --record and --out")
		}
		result, err := service.Collect(material.CollectOptions{Views: views, Records: records, Out: *destination, IncludeAssets: *includeAssets})
		if err != nil {
			return err
		}
		return emitMaterial(out, *asJSON, result)
	case "assets":
		if len(views) == 0 && *tool != "" && *session != "" && fs.NArg() == 0 {
			view, err := service.Open(material.Source{Tool: core.Tool(*tool), ID: *session}, material.ReadOptions{Limit: 1, Chars: 80})
			if err != nil {
				return err
			}
			views = append(views, view.ID)
		} else if *tool != "" || *session != "" {
			return fmt.Errorf("use --view or exact --tool/--session, not both")
		}
		if len(views) != 1 || fs.NArg() != 0 {
			return fmt.Errorf("usage: midden assets --view VIEW (or --tool TOOL --session ID) [--record ID] [--out NEW_DIRECTORY]")
		}
		result, err := service.Assets(views[0], records, *destination)
		if err != nil {
			return err
		}
		if *offset < 0 || *limit < 1 || *limit > 100 {
			return fmt.Errorf("asset page bounds: offset >=0, limit 1..100")
		}
		total := max(len(result.Assets), len(result.Omissions), len(result.Paths))
		if *offset > total {
			return fmt.Errorf("asset offset exceeds the result")
		}
		end := min(*offset+*limit, total)
		page := map[string]any{"view_id": result.ViewID, "source": result.Source, "source_digest": result.SourceDigest,
			"asset_count": result.AssetCount, "copied_count": result.CopiedCount, "copied_bytes": result.CopiedBytes,
			"path": result.Path, "omission_count": len(result.Omissions), "limitations": result.Limitations, "offset": *offset}
		for {
			page["assets"] = result.Assets[min(*offset, len(result.Assets)):min(end, len(result.Assets))]
			page["omissions"] = result.Omissions[min(*offset, len(result.Omissions)):min(end, len(result.Omissions))]
			page["paths"] = result.Paths[min(*offset, len(result.Paths)):min(end, len(result.Paths))]
			if end < total {
				page["next_offset"] = end
			} else {
				delete(page, "next_offset")
			}
			raw, _ := json.Marshal(page)
			if len(raw) <= 16<<10 {
				break
			}
			if end-*offset <= 1 {
				return fmt.Errorf("asset metadata exceeds inline bound")
			}
			end--
		}
		return emitMaterial(out, *asJSON, page)
	default:
		return fmt.Errorf("unknown material command %q", command)
	}
}

func pageView(view material.View, offset, limit int) (materialPage, error) {
	total := len(view.Records)
	if offset < 0 || offset > total || limit < 1 || limit > material.MaxRecords {
		return materialPage{}, fmt.Errorf("invalid page bounds")
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := materialPage{View: view, Offset: offset, SelectedRecords: total}
	page.Records = append([]material.Record{}, view.Records[offset:end]...)
	for {
		if end < total {
			next := end
			page.NextOffset = &next
		} else {
			page.NextOffset = nil
		}
		raw, err := json.Marshal(page)
		if err != nil {
			return page, err
		}
		if len(raw) <= 16<<10 {
			return page, nil
		}
		if len(page.Records) > 1 {
			page.Records = page.Records[:len(page.Records)-1]
			end--
			continue
		}
		return page, fmt.Errorf("one source window exceeds the inline limit; reduce --chars or use --out to save the complete result")
	}
}

func runCollection(args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(out, "Usage: midden collection inspect|read|search|select|merge|verify|export <path> [--json]\nUse <operation> --help for selection and output flags.")
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: midden collection inspect|read|search|select|merge|verify|export <path>")
	}
	action := args[0]
	fs := flag.NewFlagSet("collection "+action, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "structured output")
	destination := fs.String("out", "", "new output path")
	format := fs.String("format", "directory", "export format: directory, jsonl, markdown")
	query := fs.String("query", "", "literal text query")
	quote := fs.String("quote", "", "explicit words to check against one selected source record")
	offset := fs.Int("offset", 0, "record page offset")
	limit := fs.Int("limit", 10, "record page limit (1..256)")
	var ids repeatedStrings
	fs.Var(&ids, "record", "selected record id (repeatable)")
	if err := fs.Parse(reorderArgs(fs, args[1:])); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("a collection directory is required")
	}
	path := fs.Arg(0)
	switch action {
	case "inspect":
		if fs.NArg() != 1 {
			return fmt.Errorf("inspect accepts one collection")
		}
		m, err := material.Manifest(path)
		if err != nil {
			return err
		}
		return emitMaterial(out, *asJSON, map[string]any{"path": path, "schema": m.Schema, "record_count": m.RecordCount, "records_digest": m.RecordsDigest,
			"source_count": len(m.Sources), "asset_count": len(m.Assets), "manifest": filepath.Join(path, "manifest.json"), "warnings": m.Warnings})
	case "verify":
		if *quote != "" {
			if len(ids) != 1 {
				return fmt.Errorf("--quote requires exactly one --record")
			}
			result, err := material.MatchQuote(path, ids[0], *quote)
			if err != nil {
				return err
			}
			if err = emitMaterial(out, *asJSON, result); err != nil {
				return err
			}
			if !result.Matched {
				return fmt.Errorf("quotation does not match the selected source window")
			}
			return nil
		}
		report, err := material.Verify(path)
		if err != nil {
			return err
		}
		if err = emitMaterial(out, *asJSON, report); err != nil {
			return err
		}
		if !report.Valid {
			return fmt.Errorf("collection verification failed")
		}
		return nil
	case "read", "search":
		var rows []material.Record
		var err error
		if action == "search" {
			rows, err = material.SearchCollection(path, *query)
		} else {
			rows, err = material.ReadCollection(path)
		}
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			want := map[string]bool{}
			for _, id := range ids {
				want[id] = true
			}
			filtered := []material.Record{}
			found := map[string]bool{}
			for _, r := range rows {
				if want[r.ID] {
					filtered = append(filtered, r)
					found[r.ID] = true
				}
			}
			if len(found) != len(want) {
				return fmt.Errorf("selected record is absent from the collection result")
			}
			rows = filtered
		}
		if *offset < 0 || *offset > len(rows) || *limit < 1 || *limit > material.MaxRecords {
			return fmt.Errorf("invalid record page bounds")
		}
		end := *offset + *limit
		if end > len(rows) {
			end = len(rows)
		}
		result := map[string]any{"records": rows[*offset:end], "total": len(rows), "offset": *offset}
		for {
			if end < len(rows) {
				result["next_offset"] = end
			} else {
				delete(result, "next_offset")
			}
			raw, err := json.Marshal(result)
			if err != nil {
				return err
			}
			if len(raw) <= 16<<10 {
				break
			}
			if end-*offset <= 1 {
				return fmt.Errorf("record exceeds inline limit; use collection export to inspect the complete data file")
			}
			end--
			result["records"] = rows[*offset:end]
		}
		return emitMaterial(out, *asJSON, result)
	case "select":
		result, err := material.Select(path, ids, *destination)
		if err != nil {
			return err
		}
		return emitMaterial(out, *asJSON, result)
	case "merge":
		result, err := material.Merge(fs.Args(), *destination)
		if err != nil {
			return err
		}
		return emitMaterial(out, *asJSON, result)
	case "export":
		if *destination == "" {
			return fmt.Errorf("--out is required")
		}
		if err := material.Export(path, *destination, *format); err != nil {
			return err
		}
		return emitMaterial(out, *asJSON, map[string]any{"path": *destination, "format": *format})
	default:
		return fmt.Errorf("unknown collection operation %q", action)
	}
}

func emitMaterial(out io.Writer, asJSON bool, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > 16<<10 {
		return fmt.Errorf("result exceeds the 16 KiB inline bound; narrow the selection or inspect its data file")
	}
	encoder := json.NewEncoder(out)
	if !asJSON {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(value)
}

func writeCLIFile(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("output file is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
