package material

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/adapter"
)

func TestOpenRejectsExistingViewOnlyOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts ReadOptions
	}{
		{"records", ReadOptions{Records: []string{"claude:fixture-session:0@0-0"}}},
		{"before", ReadOptions{Before: 1}},
		{"after", ReadOptions{After: 1}},
		{"negative before", ReadOptions{Before: -1}},
		{"negative after", ReadOptions{After: -1}},
		{"combined", ReadOptions{Records: []string{"claude:fixture-session:0@0-0"}, Before: 1, After: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, source, _ := fixture(t)
			if _, err := service.Open(source, tc.opts); err == nil || !strings.Contains(err.Error(), "existing view") {
				t.Errorf("existing-view options were not rejected: %v", err)
			}
			if _, err := os.Stat(service.State); !os.IsNotExist(err) {
				t.Fatal("invalid open options created cache state")
			}
			unavailable := Service{State: filepath.Join(t.TempDir(), "state"), Roots: adapter.Roots{Strict: true}}
			if _, err := unavailable.Open(source, tc.opts); err == nil || !strings.Contains(err.Error(), "existing view") {
				t.Fatalf("source resolution happened before option validation: %v", err)
			}
		})
	}
}

func TestExistingViewStillSupportsRecordContext(t *testing.T) {
	service, source, _ := fixture(t)
	view, err := service.Open(source, ReadOptions{Limit: 12, Chars: 128})
	if err != nil {
		t.Fatal(err)
	}
	focused, err := service.Read(view.ID, ReadOptions{Records: []string{view.Records[5].ID}, Before: 1, After: 1, Limit: 3, Chars: 128})
	if err != nil || len(focused.Records) != 3 {
		t.Fatalf("existing-view context was rejected or lost: %+v %v", focused, err)
	}
	for i, record := range focused.Records {
		if record.Reference.RecordIndex != int64(4+i) {
			t.Fatal("existing-view context selected different records")
		}
	}
}
