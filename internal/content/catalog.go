// Package content owns the delivery-neutral content vocabulary.
package content

import "strings"

type Template struct {
	Name, Title, Audience, Shape, Maker, Format string
	Wants                                       []string
	RequiresModel, LegacyCLI                    bool
}

var templates = []Template{
	narrative("tutorial", "Step-by-step tutorial", "someone who has never done this before",
		"Prerequisites, numbered steps with verified commands and expected results, verification, and common failures.", "command", "decision", "error_fix", "gotcha"),
	narrative("howto", "Focused how-to", "a practitioner who wants one task done",
		"One goal, the minimal verified path to it, and nothing else.", "command", "gotcha"),
	narrative("faq", "Frequently asked questions", "someone hitting problems",
		"Question headings and concise evidence-backed answers, most common first.", "gotcha", "error_fix", "decision"),
	narrative("tsg", "Troubleshooting guide", "someone whose system is broken",
		"Symptom, diagnosis confirmation, fix, and prevention. Distinguish unresolved failures.", "error_fix", "gotcha", "dead_end"),
	narrative("adr", "Architecture decision record", "engineers maintaining the project later",
		"Context, Decision, Alternatives considered, Consequences, Evidence. State superseded decisions explicitly.", "decision", "dead_end"),
	narrative("changelog", "Changelog entry", "users of the software",
		"Added, Changed, Fixed. User-visible effects first; do not claim deployment without evidence.", "decision", "error_fix"),
	narrative("post", "Blog post", "a technical reader who does not know the project",
		"A concrete hook, the problem, what was tried, what worked, and what transfers. Be honest about failures.", "decision", "dead_end", "error_fix", "gotcha"),
	narrative("readme", "README section", "someone evaluating the project",
		"What it does, why it exists, and verified instructions for running it.", "decision", "command"),
	narrative("lessons", "Lessons learned", "the team, retrospectively",
		"Each lesson is a claim with supporting and contradicting evidence. No platitudes.", "dead_end", "gotcha", "decision", "error_fix"),
	{Name: "release_pack", Title: "Release and launch pack", Audience: "users, maintainers, and launch channels", Maker: "Midden", Format: "markdown", RequiresModel: true},
	{Name: "slides", Title: "Presentation deck", Audience: "an engineering review or workshop", Maker: "Marp", Format: "marp", RequiresModel: true},
	{Name: "diagram", Title: "Architecture diagram", Audience: "technical readers who need the system shape quickly", Maker: "D2", Format: "d2", RequiresModel: true},
	{Name: "video_brief", Title: "Video production brief", Audience: "a producer creating a concise product demonstration", Maker: "Midden / production handoff", Format: "markdown", RequiresModel: true},
	{Name: "handbook", Title: "Project field guide", Audience: "the owner returning to the project later", Maker: "Midden + Quarto/Pandoc", Format: "markdown", RequiresModel: true},
	{Name: "flashcards", Title: "Spaced-repetition deck", Audience: "the owner retaining commands, concepts, and gotchas", Maker: "Midden / Anki export", Format: "tsv", RequiresModel: true},
	{Name: "quiz", Title: "Scenario quiz", Audience: "a learner testing applied understanding", Maker: "Midden / H5P handoff", Format: "markdown", RequiresModel: true},
	{Name: "notebook_pack", Title: "Research notebook source pack", Audience: "a local research or retrieval workspace", Maker: "Midden / Open Notebook", Format: "markdown"},
	{Name: "skill", Title: "Agent skill proposal", Audience: "an operator reviewing a reusable agent behavior", Maker: "Agent Skills", Format: "markdown", RequiresModel: true},
	{Name: "instruction_patch", Title: "Instruction-file patch proposal", Audience: "an operator reviewing a scoped behavior rule", Maker: "Midden", Format: "diff", RequiresModel: true},
	{Name: "agent_profile", Title: "Specialist agent proposal", Audience: "an operator reviewing a bounded specialist", Maker: "Midden", Format: "markdown", RequiresModel: true},
	{Name: "eval_pack", Title: "Evidence-derived evaluation pack", Audience: "an operator comparing current and proposed behavior", Maker: "Midden / Promptfoo", Format: "jsonl"},
	{Name: "retrieval_pack", Title: "Retrieval memory pack", Audience: "a local RAG or durable-memory system", Maker: "Midden", Format: "jsonl"},
	{Name: "sft_pack", Title: "Supervised examples pack", Audience: "an expert reviewing possible training examples", Maker: "Midden", Format: "jsonl"},
	{Name: "preference_pack", Title: "Preference-pair pack", Audience: "an expert reviewing accepted versus rejected approaches", Maker: "Midden", Format: "jsonl"},
	{Name: "privacy_manifest", Title: "Privacy and licensing manifest", Audience: "the owner auditing a data export", Maker: "Midden", Format: "json"},
	{Name: "provenance_manifest", Title: "Bundle provenance manifest", Audience: "the owner auditing every derived claim", Maker: "Midden", Format: "json"},
}

func narrative(name, title, audience, shape string, wants ...string) Template {
	return Template{Name: name, Title: title, Audience: audience, Shape: shape, Wants: wants,
		Maker: "Midden", Format: "markdown", RequiresModel: true, LegacyCLI: true}
}

func Templates() []Template {
	out := append([]Template(nil), templates...)
	for i := range out {
		out[i].Wants = append([]string(nil), out[i].Wants...)
	}
	return out
}

func Find(kind string) (Template, bool) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	for _, t := range Templates() {
		if t.Name == kind {
			return t, true
		}
	}
	return Template{}, false
}
