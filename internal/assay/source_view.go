package assay

// SourceView identifies the immutable prefix that an investigation observes.
// New records beyond this boundary belong to a subsequent explicit refresh.
type SourceView struct {
	Kind    string `json:"kind"`
	Bytes   int64  `json:"bytes"`
	Records int64  `json:"records"`
}

func (s *Scanner) ViewBoundary() *SourceView { return s.selection.View }
func (s *Scanner) ObservedRecords() int64    { return s.m.TotalRecords }
