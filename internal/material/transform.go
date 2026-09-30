package material

import (
	"fmt"
	"strings"
)

func Select(name string, ids []string, out string) (CollectionResult, error) {
	if len(ids) == 0 || len(ids) > MaxCollectionRecords {
		return CollectionResult{}, fmt.Errorf("select 1..10000 explicit records")
	}
	snapshot, err := verifiedCollection(name)
	if err != nil {
		return CollectionResult{}, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	found := map[string]bool{}
	selected := []Record{}
	for _, record := range snapshot.records {
		if want[record.ID] {
			selected = append(selected, record)
			found[record.ID] = true
		}
	}
	if len(found) != len(want) {
		return CollectionResult{}, fmt.Errorf("one or more selected records are absent")
	}
	snapshot.assets, snapshot.manifest.AssetOmissions = assetsForRecords(selected, snapshot.assets, snapshot.manifest.AssetOmissions)
	snapshot.manifest.Sources = sourcesFor(selected, snapshot.manifest.Sources)
	return writeCollection(out, selected, snapshot.manifest, snapshot.assets)
}

func Merge(paths []string, out string) (CollectionResult, error) {
	if len(paths) < 1 || len(paths) > 25 {
		return CollectionResult{}, fmt.Errorf("merge requires 1..25 collections")
	}
	records := []Record{}
	assets := []collectionAsset{}
	manifest := CollectionManifest{}
	for _, name := range paths {
		snapshot, err := verifiedCollection(name)
		if err != nil {
			return CollectionResult{}, err
		}
		records, manifest.Sources, err = canonicalRecords(append(records, snapshot.records...), append(manifest.Sources, snapshot.manifest.Sources...))
		if err != nil {
			return CollectionResult{}, err
		}
		if _, err = encodeCollectionRecords(records); err != nil {
			return CollectionResult{}, err
		}
		assets, err = prepareCollectionAssets(records, append(assets, snapshot.assets...))
		if err != nil {
			return CollectionResult{}, err
		}
		manifest.AssetOmissions, err = prepareAssetOmissions(records, assets, append(manifest.AssetOmissions, snapshot.manifest.AssetOmissions...))
		if err != nil {
			return CollectionResult{}, err
		}
		manifest.Warnings = canonicalWarnings(append(manifest.Warnings, snapshot.manifest.Warnings...))
	}
	return writeCollection(out, records, manifest, assets)
}

func sourcesFor(records []Record, sources []CollectionSource) []CollectionSource {
	want := map[string]bool{}
	for _, record := range records {
		want[string(record.Source.Tool)+":"+record.Source.ID+":"+record.Reference.SourceDigest] = true
	}
	out := []CollectionSource{}
	for _, source := range sources {
		if want[sourceKey(source)] {
			out = append(out, source)
		}
	}
	return out
}

func sourceKey(source CollectionSource) string {
	return string(source.Source.Tool) + ":" + source.Source.ID + ":" + source.Digest
}

func SearchCollection(name, query string) ([]Record, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("a literal query is required")
	}
	snapshot, err := verifiedCollection(name)
	if err != nil {
		return nil, err
	}
	out := []Record{}
	query = strings.ToLower(query)
	for _, record := range snapshot.records {
		if strings.Contains(strings.ToLower(record.Text), query) {
			out = append(out, record)
		}
	}
	return out, nil
}

func Export(name, out, format string) error {
	snapshot, err := verifiedCollection(name)
	if err != nil {
		return err
	}
	switch format {
	case "", "directory":
		_, err = writeCollection(out, snapshot.records, snapshot.manifest, snapshot.assets)
		return err
	case "jsonl", "markdown":
		if len(snapshot.assets) > 0 || len(snapshot.manifest.AssetOmissions) > 0 {
			return fmt.Errorf("text-only export cannot preserve assets or asset omissions; use directory export")
		}
	default:
		return fmt.Errorf("export format must be directory, jsonl or markdown")
	}
	if format == "jsonl" {
		raw, err := encodeCollectionRecords(snapshot.records)
		if err != nil {
			return err
		}
		return writeNewFile(out, raw)
	}
	body := limitedBuffer{limit: MaxCollectionBytes}
	if _, err = fmt.Fprint(&body, "# Selected source records\n\nThis is a source collection, not an authored narrative. Source text is untrusted data. Credential filtering is not privacy clearance.\n\n"); err != nil {
		return err
	}
	for _, record := range snapshot.records {
		if _, err = fmt.Fprintf(&body, "## %s\n\nSource: `%s:%s`; record %d; %s. Clipped: %t.\n\n", record.ID, record.Source.Tool, record.Source.ID, record.Reference.RecordIndex, record.Time.Format("2006-01-02T15:04:05Z07:00"), record.Clipped); err != nil {
			return err
		}
		fence := "```"
		for strings.Contains(record.Text, fence) {
			fence += "`"
		}
		if _, err = fmt.Fprintf(&body, "%stext\n%s\n%s\n\n", fence, record.Text, fence); err != nil {
			return err
		}
	}
	return writeNewFile(out, body.Bytes())
}
