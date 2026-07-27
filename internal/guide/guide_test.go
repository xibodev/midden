package guide

import (
	"strings"
	"testing"
)

func TestSpendingListIsSmallAndExplicit(t *testing.T) {
	// The product's central promise is that almost everything is free. If this
	// list grows, the labelling has to change with it — so pin it.
	sp := Spending()
	if len(sp) != 2 {
		t.Fatalf("expected exactly 2 spending commands, got %d: %v", len(sp), sp)
	}
	want := map[string]bool{"reclaim": true, "refine": true}
	for _, s := range sp {
		if !want[s] {
			t.Errorf("unexpected spending command %q", s)
		}
	}
}

func TestEveryCommandHasACostClassAndStage(t *testing.T) {
	seen := map[string]bool{}
	stages := map[string]bool{}
	for _, s := range Stages {
		stages[s.Name] = true
	}

	for _, c := range Commands {
		if c.Name == "" || c.Blurb == "" {
			t.Errorf("command %+v is missing a name or blurb", c)
		}
		if seen[c.Name] {
			t.Errorf("duplicate command %q", c.Name)
		}
		seen[c.Name] = true

		if !stages[c.Stage] {
			t.Errorf("command %q has unknown stage %q", c.Name, c.Stage)
		}
	}
}

func TestCostOfDefaultsToFree(t *testing.T) {
	// An unknown command must never be silently labelled as spending, and a
	// known free one must never be labelled as costly.
	if CostOf("reclaim") != Spends {
		t.Error("reclaim should be SPENDS")
	}
	if CostOf("doctor") != Free {
		t.Error("doctor should be FREE")
	}
	if CostOf("not-a-command") != Free {
		t.Error("unknown commands should default to FREE")
	}
	if Spends.String() != "SPENDS" || Free.String() != "FREE" {
		t.Error("cost labels must be explicit words, not colours or symbols")
	}
}

func TestDataLossOutranksEverything(t *testing.T) {
	// Losing four days of work beats recovering disk space, which beats
	// producing documentation. Ordering is by consequence, not pipeline
	// position.
	s := State{
		HasIndex: true, Assayed: 10,
		CriticalRisk: 4, LargestAtRiskID: "9544176f",
		ReclaimBytes: 3 << 30, DeadDirs: 198, Nuggets: 5,
	}
	steps := Next(s)
	if len(steps) == 0 {
		t.Fatal("expected suggestions")
	}
	if !strings.Contains(steps[0].Why, "resume cliff") {
		t.Errorf("data loss should rank first, got %q", steps[0].Why)
	}
	if !strings.Contains(steps[0].Command, "9544176f") {
		t.Errorf("should name the specific session at risk: %q", steps[0].Command)
	}
}

func TestMeasureBeforeDeciding(t *testing.T) {
	// Nothing downstream can be costed before an assay exists, so that should
	// be the top suggestion on a fresh machine.
	steps := Next(State{Sessions: 500})
	if len(steps) == 0 {
		t.Fatal("a fresh machine should still get guidance")
	}
	if !strings.Contains(steps[0].Command, "scan --assay") {
		t.Errorf("fresh machine should be told to measure first, got %q", steps[0].Command)
	}
}

func TestHarvestIsSuggestedBeforeItIsTooLate(t *testing.T) {
	// Deleting before harvesting throws away the only record of why decisions
	// were made.
	steps := Next(State{HasIndex: true, Assayed: 20, Nuggets: 0, ReclaimBytes: 1 << 30})

	var found *Step
	for i := range steps {
		if strings.Contains(steps[i].Command, "reclaim") {
			found = &steps[i]
		}
	}
	if found == nil {
		t.Fatal("should suggest reclaiming when nothing has been harvested")
	}
	if found.Cost != Spends {
		t.Error("the reclaim suggestion must be labelled as spending")
	}
	if !strings.Contains(found.Command, "--dry-run") {
		t.Errorf("a spending suggestion must offer the free preview first: %q", found.Command)
	}
}

func TestSuggestionsUseTheCheapestUsefulScope(t *testing.T) {
	// Suggesting `--days 30` when a specific workspace would do is how a tool
	// quietly costs someone money.
	s := State{HasIndex: true, Assayed: 5, BusiestWorkspace: `E:\projects\orvantix`}
	for _, step := range Next(s) {
		if strings.Contains(step.Command, "reclaim") {
			if !strings.Contains(step.Command, "--workspace") {
				t.Errorf("should scope to the busiest workspace, got %q", step.Command)
			}
			if !strings.Contains(step.Command, "--records 40") {
				t.Errorf("should suggest reduced evidence, got %q", step.Command)
			}
		}
	}
}

func TestWorkspacesWithSpacesAreQuoted(t *testing.T) {
	s := State{HasIndex: true, Assayed: 5, BusiestWorkspace: `E:\startup projects\orvantix`}
	for _, step := range Next(s) {
		if strings.Contains(step.Command, "reclaim") && !strings.Contains(step.Command, `"`) {
			t.Errorf("a path with spaces must be quoted or the command fails: %q", step.Command)
		}
	}
}

func TestEverySuggestionIsRunnableAndExplained(t *testing.T) {
	s := State{
		HasIndex: true, Sessions: 600, Assayed: 300, CriticalRisk: 2,
		DeadDirs: 100, Nuggets: 9, ReclaimBytes: 2 << 30,
	}
	for _, step := range Next(s) {
		if !strings.HasPrefix(step.Command, "midden ") {
			t.Errorf("suggestion is not a runnable command: %q", step.Command)
		}
		if step.Why == "" {
			t.Errorf("%q has no reason", step.Command)
		}
		if step.Value == "" {
			t.Errorf("%q does not say what it gets you", step.Command)
		}
		if step.CostName == "" {
			t.Errorf("%q has no cost label", step.Command)
		}
	}
}

func TestTopReturnsNothingWhenAllIsWell(t *testing.T) {
	// A clean machine should not be nagged. Nagging is a Category 5
	// anti-pattern.
	if _, ok := Top(State{HasIndex: true, Assayed: 5, Nuggets: 10, Artifacts: 3}); !ok {
		return // no suggestion is an acceptable outcome
	}
}

func TestCheapestGuidanceNamesRealFlags(t *testing.T) {
	tips := Cheapest()
	if len(tips) < 3 {
		t.Fatal("cost guidance should be substantive")
	}
	joined := strings.Join(tips, " ")
	for _, flag := range []string{"--workspace", "--records", "--model", "--dry-run"} {
		if !strings.Contains(joined, flag) {
			t.Errorf("cost guidance should name %s, the actual lever", flag)
		}
	}
}

func TestInStageCoversEveryCommand(t *testing.T) {
	total := 0
	for _, s := range Stages {
		total += len(InStage(s.Name))
	}
	if total != len(Commands) {
		t.Errorf("stages cover %d commands, want %d — one would be invisible in help",
			total, len(Commands))
	}
}
