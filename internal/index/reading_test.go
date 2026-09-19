package index

import (
	"encoding/json"
	"testing"
)

func TestReadingBudgetIsCumulativeAndRetrySafe(t *testing.T) {
	db, err := OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := db.PutReadingPacket("p1", "d1", "inv", json.RawMessage(`{"packet":1}`), map[string]int{"r1": 1000})
	if err != nil || first.UsedBytes != 1000 {
		t.Fatal(first, err)
	}
	retry, err := db.PutReadingPacket("p1", "d1", "inv", json.RawMessage(`{"packet":1}`), map[string]int{"r1": 1000})
	if err != nil || retry.UsedBytes != 1000 {
		t.Fatal("retry charged twice", retry, err)
	}
	more, err := db.PutReadingPacket("p2", "d2", "inv", json.RawMessage(`{"packet":2}`), map[string]int{"r1": 2000, "r2": 3000})
	if err != nil || more.UsedBytes != 5000 {
		t.Fatal("prefix extension counted incorrectly", more, err)
	}
	if _, err = db.PutReadingPacket("oversized", "d3", "inv", json.RawMessage(`{}`), map[string]int{"r3": DefaultReadingBudget}); err == nil {
		t.Fatal("budget exceeded")
	}
	if _, err = db.ReadingPacket("oversized"); err == nil {
		t.Fatal("failed read persisted")
	}
	b, err := db.ReadingBudget("inv")
	if err != nil || b.UsedBytes != 5000 {
		t.Fatal("failed read consumed budget", b, err)
	}
}
