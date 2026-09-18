package editorial

func array[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func (a *Analysis) normalize() {
	a.Arcs = array(a.Arcs)
	a.Decisions = array(a.Decisions)
	a.Claims = array(a.Claims)
	a.Assets = array(a.Assets)
	a.Gaps = array(a.Gaps)
	a.Opportunities = array(a.Opportunities)
	a.Chapters = array(a.Chapters)
	for i := range a.Claims {
		a.Claims[i].SupportingIDs = array(a.Claims[i].SupportingIDs)
	}
	for i := range a.Opportunities {
		a.Opportunities[i].ClaimIDs = array(a.Opportunities[i].ClaimIDs)
		a.Opportunities[i].Risks = array(a.Opportunities[i].Risks)
	}
}
