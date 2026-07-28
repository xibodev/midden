package guide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBusiestScopeRejectsHome guards the suggestion that quietly widened the
// blast radius: the home directory has the most sessions on a real machine,
// because that is where a CLI starts when it is not started in a project.
// Proposing it as a --workspace value contradicts the tool's own first piece
// of cost advice.
func TestBusiestScopeRejectsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	project := filepath.Join("E:", "startup projects", "orvantix")

	got := BusiestScope(map[string]int{
		home:    400, // most sessions, but useless as a scope
		project: 12,
	})
	if got != project {
		t.Errorf("BusiestScope = %q, want the project directory %q", got, project)
	}
}

func TestBusiestScopeRejectsShellFolders(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	project := filepath.Join("E:", "work", "api")
	for _, junk := range []string{"Desktop", "Downloads", "Documents", "temp"} {
		t.Run(junk, func(t *testing.T) {
			got := BusiestScope(map[string]int{
				filepath.Join(home, junk): 99,
				project:                   3,
			})
			if got != project {
				t.Errorf("BusiestScope = %q, want %q", got, project)
			}
		})
	}
}

func TestBusiestScopeRejectsRoots(t *testing.T) {
	for _, root := range []string{`C:\`, "C:", "/"} {
		if scopeworthy(root) {
			t.Errorf("scopeworthy(%q) = true, want false", root)
		}
	}
}

// TestBusiestScopeEmptyWhenNothingUseful confirms no suggestion is preferred
// over a bad one: if every candidate is the home directory, the caller should
// fall back to a time-scoped command rather than propose matching everything.
func TestBusiestScopeEmptyWhenNothingUseful(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := BusiestScope(map[string]int{home: 500}); got != "" {
		t.Errorf("BusiestScope = %q, want empty", got)
	}
}

// TestNextKeepsMiningAfterFirstArtifact guards the terminal state: succeeding
// once used to remove the value-producing loop from the guidance entirely,
// leaving only housekeeping at the exact moment the tool had proved it worked.
func TestNextKeepsMiningAfterFirstArtifact(t *testing.T) {
	steps := Next(State{
		HasIndex:  true,
		Sessions:  600,
		Assayed:   300,
		Nuggets:   15,
		Artifacts: 1,
	})
	if len(steps) == 0 {
		t.Fatal("no steps suggested")
	}

	var joined []string
	for _, s := range steps {
		joined = append(joined, s.Command)
	}
	all := strings.Join(joined, " | ")

	if !strings.Contains(all, "catalog") {
		t.Errorf("no path back into the mining loop after first artifact; got %s", all)
	}
}

// TestNextRanksDataLossFirst is the ordering promise the UI makes explicit:
// losing work outranks saving disk, which outranks writing docs.
func TestNextRanksDataLossFirst(t *testing.T) {
	steps := Next(State{
		HasIndex:        true,
		Sessions:        600,
		Assayed:         300,
		CriticalRisk:    4,
		AtRisk:          5,
		DeadDirs:        198,
		ReclaimBytes:    3 << 30,
		LargestAtRiskID: "9544176f",
	})
	if len(steps) == 0 {
		t.Fatal("no steps suggested")
	}
	if !strings.Contains(steps[0].Command, "brief") {
		t.Errorf("top step = %q, want the handoff rescue first", steps[0].Command)
	}
	if steps[0].Cost != Free {
		t.Errorf("the most urgent action costs %v, want Free", steps[0].Cost)
	}
}

// Spending is already covered by TestSpendingListIsSmallAndExplicit in
// guide_test.go, so it is not re-asserted here.
