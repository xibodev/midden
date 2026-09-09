package module

import "testing"

// TestV2PayloadCarriesEveryV1Field pins ADDITIVE, which is the property the
// frozen contract rests on and the one my v2 failed.
//
// v2 is additive and v1 modules keep working, so a v2 descriptor is a v1
// descriptor PLUS fields -- one payload valid under both validators. My first
// v2 dropped protocol_versions, artifact_schemas and skills; they marshalled
// null, which the never-null guarantee forbids, and the host's v1 validator
// reported 13 violations.
//
// The v2 gate could not see it: it does not check v1 shape. A module can be
// perfectly conformant to v2 and unusable by a v1 host, and only running BOTH
// validators against ONE payload finds that.
func TestV2PayloadCarriesEveryV1Field(t *testing.T) {
	v1 := Describe()
	v2 := DescribeV2()

	if len(v2.ProtocolVersions) == 0 {
		t.Error("v2 drops protocol_versions; a v1 host refuses null")
	}
	if v2.ArtifactSchemas == nil {
		t.Error("v2 drops the artifact_schemas map, so a capability reference " +
			"to a schema resolves against nothing on the v1 path")
	}
	if len(v2.Capabilities) != len(v1.Capabilities) {
		t.Fatalf("v2 publishes %d capabilities, v1 publishes %d",
			len(v2.Capabilities), len(v1.Capabilities))
	}

	for _, c := range v2.Capabilities {
		if c.ArtifactSchemas == nil {
			t.Errorf("capability %s: artifact_schemas is nil and marshals null", c.ID)
		}
		if c.Skills == nil {
			t.Errorf("capability %s: skills is nil and marshals null", c.ID)
		}
	}

	// Every schema a capability references must resolve in the carried map,
	// which is what the v1 validator checks and what caught the second round.
	for _, c := range v2.Capabilities {
		for _, ref := range c.ArtifactSchemas {
			if _, ok := v2.ArtifactSchemas[ref]; !ok {
				t.Errorf("capability %s references artifact schema %q, which the "+
					"v2 payload does not carry", c.ID, ref)
			}
		}
	}
}

// TestV2IsPublishedWithoutBeingAsked pins the handshake.
//
// The host sends no flag. A flag-gated v2 is a second document behind a second
// call, which satisfies "v1 modules keep working" only by publishing two
// different things -- not additive. My v2 was opt-in and therefore invisible to
// the host it was built for.
func TestV2IsPublishedWithoutBeingAsked(t *testing.T) {
	if DescribeV2().ContractVersion != ContractV2 {
		t.Errorf("the default v2 projection declares %q, want %q",
			DescribeV2().ContractVersion, ContractV2)
	}
}
