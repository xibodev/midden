package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skillInfo is one installed skill, as its SKILL.md introduces it.
type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// listSkills reads the name and description of every skill in dir: each
// folder whose SKILL.md opens with them.
func listSkills(dir string) []skillInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []skillInfo{}
	}
	skills := []skillInfo{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := readBounded(filepath.Join(dir, entry.Name(), "SKILL.md"), 1<<20)
		if err != nil {
			continue
		}
		if skill, ok := skillFrontmatter(raw); ok {
			skills = append(skills, skill)
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

// skillFrontmatter reads name and description from the frontmatter that
// opens a SKILL.md.
func skillFrontmatter(raw []byte) (skillInfo, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	var skill skillInfo
	for line := 0; scanner.Scan(); line++ {
		text := strings.TrimRight(scanner.Text(), "\r")
		if line == 0 {
			if text != "---" {
				return skill, false
			}
			continue
		}
		if text == "---" {
			return skill, skill.Name != ""
		}
		key, value, ok := strings.Cut(text, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			skill.Name = value
		case "description":
			skill.Description = value
		}
	}
	return skill, false
}
