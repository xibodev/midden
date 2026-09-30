package dispose

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Verification is the evidence that a pruned transcript is still usable.
//
// Nothing is ever deleted on the strength of "the prune ran without error".
// A rewritten transcript must prove it kept every record, kept every link,
// and kept the conversation.
type Verification struct {
	OK bool `json:"ok"`

	SourceRecords int64 `json:"source_records"`
	TargetRecords int64 `json:"target_records"`

	SourceSignal int64 `json:"source_signal_records"`
	TargetSignal int64 `json:"target_signal_records"`

	SourceIDs int64 `json:"source_ids"`
	TargetIDs int64 `json:"target_ids"`

	ParseErrors int64    `json:"parse_errors"`
	Failures    []string `json:"failures,omitempty"`
}

func (v *Verification) fail(format string, a ...any) {
	v.OK = false
	v.Failures = append(v.Failures, fmt.Sprintf(format, a...))
}

// Verify compares a pruned transcript against its source.
//
// The checks are deliberately strict and structural rather than semantic:
//   - every record survives (count must match exactly)
//   - every record still parses as JSON
//   - every identifier survives, so the parentUuid/id chain is intact
//   - every signal-class record survives, so no conversation was lost
func Verify(source, target string, kindOf func([]byte) string) (*Verification, error) {
	v := &Verification{OK: true}

	src, err := profile(source, kindOf)
	if err != nil {
		return nil, fmt.Errorf("profile source: %w", err)
	}
	dst, err := profile(target, kindOf)
	if err != nil {
		return nil, fmt.Errorf("profile target: %w", err)
	}

	v.SourceRecords, v.TargetRecords = src.records, dst.records
	v.SourceSignal, v.TargetSignal = src.signal, dst.signal
	v.SourceIDs, v.TargetIDs = src.ids, dst.ids
	v.ParseErrors = dst.parseErrors

	if dst.records != src.records {
		v.fail("record count changed: %d -> %d (pruning must preserve every record)",
			src.records, dst.records)
	}
	if dst.parseErrors > src.parseErrors {
		v.fail("introduced %d unparseable records", dst.parseErrors-src.parseErrors)
	}
	if dst.ids != src.ids {
		v.fail("identifier count changed: %d -> %d (record chain would break)",
			src.ids, dst.ids)
	}
	if dst.signal != src.signal {
		v.fail("signal records changed: %d -> %d (conversation was lost)",
			src.signal, dst.signal)
	}
	if dst.bytes >= src.bytes {
		v.fail("target is not smaller (%d >= %d): nothing was recovered", dst.bytes, src.bytes)
	}
	return v, nil
}

type fileProfile struct {
	records     int64
	signal      int64
	ids         int64
	parseErrors int64
	bytes       int64
}

func profile(path string, kindOf func([]byte) string) (fileProfile, error) {
	var p fileProfile

	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()

	if fi, err := f.Stat(); err == nil {
		p.bytes = fi.Size()
	}

	r := bufio.NewReaderSize(f, 1<<20)
	var line []byte
	for {
		chunk, rerr := r.ReadSlice('\n')
		if rerr == bufio.ErrBufferFull {
			line = append(line, chunk...)
			continue
		}
		line = append(line, chunk...)

		if len(strings.TrimSpace(string(line))) > 0 {
			p.records++

			var rec map[string]json.RawMessage
			if json.Unmarshal(line, &rec) != nil {
				p.parseErrors++
			} else {
				for _, key := range []string{"id", "uuid", "parentId", "parentUuid"} {
					if _, ok := rec[key]; ok {
						p.ids++
					}
				}
				kind := kindOf(line)
				if isSignalKind(kind) {
					p.signal++
				}
			}
		}
		line = line[:0]

		if rerr != nil {
			return p, nil
		}
	}
}

// isSignalKind is a local check so verification does not depend on the
// classifier it is meant to police.
func isSignalKind(kind string) bool {
	switch kind {
	case "user.message", "assistant.message", "user", "assistant",
		"summary", "text", "reasoning", "skill.invoked":
		return true
	}
	return false
}
