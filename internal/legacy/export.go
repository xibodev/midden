// Package legacy exports old Midden working data without migrating its store.
package legacy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

type Report struct {
	Path     string   `json:"path"`
	Tables   []string `json:"tables"`
	Rows     int      `json:"rows"`
	Files    int      `json:"files"`
	Warnings []string `json:"warnings"`
}

var tables = []string{"nuggets", "artifacts", "editorial_projects", "editorial_revisions", "editorial_project_revisions", "editorial_recipe_scope", "refinery_recipes", "refinery_runs", "refinery_outputs", "reading_packets", "host_reviews"}

func omittedTables(tx *sql.Tx) ([]string, error) {
	known := map[string]bool{}
	for _, name := range tables {
		known[name] = true
	}
	rows, err := tx.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		if !known[name] {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

func Export(source, out string) (Report, error) {
	report := Report{Tables: []string{}, Warnings: []string{"Legacy editorial statuses are exported as historical data, not new approval or truth claims."}}
	if strings.TrimSpace(source) == "" || strings.TrimSpace(out) == "" {
		return report, fmt.Errorf("legacy export requires explicit --from and --out paths")
	}
	root, err := filepath.Abs(source)
	if err != nil {
		return report, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return report, err
	}
	destination, err := filepath.Abs(out)
	if err != nil {
		return report, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return report, fmt.Errorf("export parent must exist and resolve safely: %w", err)
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	if rel, e := filepath.Rel(root, destination); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return report, fmt.Errorf("export destination must be outside the legacy store")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "index.db"))+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		return report, err
	}
	defer db.Close()
	if err = db.Ping(); err != nil {
		return report, err
	}
	tx, err := db.Begin()
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	omitted, err := omittedTables(tx)
	if err != nil {
		return report, err
	}
	if len(omitted) > 0 {
		report.Warnings = append(report.Warnings, "Tables outside the legacy working-data export contract were not copied: "+strings.Join(omitted, ", "))
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return report, err
	}
	report.Path = destination
	if err = os.Mkdir(filepath.Join(destination, "tables"), 0700); err != nil {
		return report, err
	}
	files := map[string]bool{}
	for _, table := range tables {
		var exists int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			return report, err
		}
		if exists == 0 {
			continue
		}
		rows, err := tx.Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			return report, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return report, err
		}
		file, err := os.OpenFile(filepath.Join(destination, "tables", table+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			rows.Close()
			return report, err
		}
		writer := &limitedWriter{writer: file, remaining: 64 << 20}
		encoder := json.NewEncoder(writer)
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				file.Close()
				rows.Close()
				return report, err
			}
			record := map[string]any{}
			for i, column := range columns {
				value := values[i]
				if raw, ok := value.([]byte); ok {
					value = string(raw)
				}
				record[column] = value
				if column == "path" || column == "provenance_path" || column == "rendered_path" {
					if p, ok := value.(string); ok && p != "" {
						files[p] = true
					}
				}
			}
			if err = encoder.Encode(record); err != nil {
				file.Close()
				rows.Close()
				return report, err
			}
			report.Rows++
		}
		if err = rows.Err(); err != nil {
			file.Close()
			rows.Close()
			return report, err
		}
		rows.Close()
		if err = file.Close(); err != nil {
			return report, err
		}
		report.Tables = append(report.Tables, table)
	}
	if err = tx.Commit(); err != nil {
		return report, err
	}
	paths := []string{}
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var total int64
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			report.Warnings = append(report.Warnings, "Recorded artifact unavailable: "+filepath.Base(path))
			continue
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			report.Warnings = append(report.Warnings, "Recorded artifact outside the legacy store was not copied: "+filepath.Base(path))
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return report, err
		}
		if !info.Mode().IsRegular() || info.Size() > 32<<20 || total+info.Size() > 256<<20 {
			return report, fmt.Errorf("legacy artifact exceeds bounded export limits")
		}
		target := filepath.Join(destination, "files", relative)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return report, err
		}
		in, err := os.Open(resolved)
		if err != nil {
			return report, err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			return report, err
		}
		n, copyErr := io.Copy(output, io.LimitReader(in, info.Size()+1))
		in.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return report, copyErr
		}
		if closeErr != nil {
			return report, closeErr
		}
		if n != info.Size() {
			return report, fmt.Errorf("legacy artifact changed during export")
		}
		total += n
		report.Files++
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	if err = os.WriteFile(filepath.Join(destination, "manifest.json"), append(raw, '\n'), 0600); err != nil {
		return report, err
	}
	return report, nil
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("legacy table exceeds 64 MiB; export a scoped copy instead")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}
