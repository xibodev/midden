// Package create owns Midden's planning and production semantics.
//
// It exists because significant product behaviour -- assessing evidence,
// designing a recipe from a request, revising it, approving it, and producing
// artifacts from it -- was reachable only through the web face. That was an
// accident of where the code was first needed, not a decision: measured, the
// recipe lifecycle functions contained ZERO HTTP coupling. They were product
// logic wearing a *Server receiver.
//
// A capability that exists only because one face happens to expose it is not a
// product capability. Web, CLI, module and a future in-process kernel must all
// reach the same creation semantics, or Midden means something different
// depending on how it was launched.
package create

// Progress reports what a long operation is doing.
//
// This is the ONLY thing a face injects. Production takes minutes and spends
// money, so a user needs to see movement -- but "how movement is shown" is a
// face concern: the web has a job store, a CLI prints, a kernel streams into a
// conversation, a test records. None of that is product semantics.
//
// The web Jobs type deliberately does NOT move into core. It carries job ids,
// estimates and lifecycle that only the web has.
type Progress interface {
	// Stage reports that a named phase began or advanced. Detail is
	// human-readable and may be empty.
	Stage(name, detail string)

	// Note reports something worth surfacing that is not a stage change --
	// a warning, a recovered failure, an accounting miss.
	Note(text string)
}

// NopProgress discards every report.
//
// Explicit rather than a nil check at each call site: a nil sink that panics in
// production is a worse failure than one that says nothing, and "no reporting"
// is a legitimate choice for a batch or deterministic path.
type NopProgress struct{}

func (NopProgress) Stage(string, string) {}
func (NopProgress) Note(string)          {}

// RecordingProgress captures reports for tests and for callers that want to
// replay what happened after the fact.
type RecordingProgress struct {
	Stages []StageEvent
	Notes  []string
}

// StageEvent is one recorded stage transition.
type StageEvent struct {
	Name   string
	Detail string
}

func (r *RecordingProgress) Stage(name, detail string) {
	r.Stages = append(r.Stages, StageEvent{Name: name, Detail: detail})
}

func (r *RecordingProgress) Note(text string) { r.Notes = append(r.Notes, text) }
