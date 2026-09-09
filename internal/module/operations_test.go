package module

import "testing"

// TestProjectionDoesNotWeakenEffects is RFC v2 §2's no-weakening rule, checked
// mechanically rather than by reading.
//
// A capability projecting an Operation must not declare effects weaker than the
// Operation's. This is the rule whose violation is INVISIBLE at the layer being
// reviewed: the Operation reads correct while the gate, which reads capability
// effects, fires on nothing.
//
// It is not hypothetical here. content.produce declared Local:true while able to
// spawn a model and spend -- well-formed, wrong, and it survived review. A
// chargeability fact lost between an Operation and its capability fails the same
// way and would be just as invisible.
func TestProjectionDoesNotWeakenEffects(t *testing.T) {
	d := Describe()
	for _, c := range d.Capabilities {
		opID, mapped := capabilityOperations[c.ID]
		if !mapped {
			t.Errorf("capability %q maps to no Operation and is not recorded as "+
				"projecting none; every capability must be accounted for", c.ID)
			continue
		}
		if opID == "" {
			continue // registry read; checked by the effects test below
		}
		op, ok := OperationByID(opID)
		if !ok {
			t.Errorf("capability %q claims Operation %q, which does not exist", c.ID, opID)
			continue
		}

		// A charging Operation must not be projected as priced-and-known: the
		// host gates on cost_known, so declaring it true silences the gate.
		if op.MayCharge && c.Effects.CostKnown {
			t.Errorf("%s projects %s (may charge) but declares cost_known:true; "+
				"the gate would fire on nothing", c.ID, op.ID)
		}
		// A charging Operation reaches a provider, so it is not local work.
		if op.MayCharge && c.Effects.Local {
			t.Errorf("%s projects %s (may charge) but declares local:true; "+
				"this is the shape of the shipped content.produce defect", c.ID, op.ID)
		}
		if !op.ExternalWrites && c.Effects.ExternalWrites {
			t.Errorf("%s declares external writes its Operation %s does not", c.ID, op.ID)
		}
	}
}

// TestEveryCapabilityCarriesEffects guards §2's first consequence: a capability
// projecting no Operation is not thereby outside the effects model. The host
// gate reads capability effects, so an unregulated capability is still callable.
func TestEveryCapabilityCarriesEffects(t *testing.T) {
	d := Describe()
	for _, c := range d.Capabilities {
		if opID := capabilityOperations[c.ID]; opID == "" {
			// Registry reads transform no product material, so they must be
			// free, local and non-writing. Anything else means the mapping is
			// wrong, not that the declaration is.
			if !c.Effects.Local || !c.Effects.CostKnown || c.Effects.ExternalWrites {
				t.Errorf("%s projects no Operation but declares non-trivial effects "+
					"(local=%v cost_known=%v writes=%v); either it does real work "+
					"and needs an Operation, or the declaration is wrong",
					c.ID, c.Effects.Local, c.Effects.CostKnown, c.Effects.ExternalWrites)
			}
		}
	}
}

// TestChargeabilityIsNotModelUsage pins operator ruling 13 in code.
//
// mine_evidence and produce_content both use a model AND may charge, so the two
// facts coincide today and a future reader could reasonably conclude one implies
// the other. It does not: a harness the user is already signed in to owns its own
// billing. The fields must stay separately sourced.
func TestChargeabilityIsNotModelUsage(t *testing.T) {
	op, ok := OperationByID("produce_content")
	if !ok {
		t.Fatal("produce_content missing from the inventory")
	}
	if op.ChargeDependsOn != "kind" {
		t.Errorf("produce_content chargeability must depend on kind, got %q; "+
			"seven of nineteen kinds spend nothing and a bare boolean over-gates them",
			op.ChargeDependsOn)
	}
}

// TestEveryArtifactKindIsProducedBySomeOperation pins the REVERSE REFERENCE.
//
// facet-studio's v2 validation splits "no Operations and no kinds" (legitimate,
// a module with nothing to publish) from HALF-PUBLISHED: artifact kinds exist to
// be named by an Operation, so a kind nobody produces is an incomplete payload a
// host may refuse.
//
// Checked here rather than discovered when the v2 payload is built, because the
// obligation is on the OPERATION MODEL and the kinds already exist. A kind
// declared today with no producer is a defect that stays invisible until the
// wire is wired.
func TestEveryArtifactKindIsProducedBySomeOperation(t *testing.T) {
	produced := map[string]bool{}
	for _, op := range Operations {
		for _, k := range op.Produces {
			produced[k] = true
		}
	}

	d := Describe()
	if len(d.ArtifactSchemas) == 0 {
		t.Fatal("no artifact schemas declared; the check asserts nothing")
	}
	for kind := range d.ArtifactSchemas {
		if !produced[kind] {
			t.Errorf("artifact kind %q is declared but no Operation produces it; "+
				"that is a half-published payload rather than a harmless extra",
				kind)
		}
	}
}

// TestNoOperationProducesAnUndeclaredKind checks the FORWARD direction.
//
// An Operation naming a kind that does not exist is the same defect inverted:
// the reference resolves to nothing, and a consumer following it finds no
// contract to validate against.
func TestNoOperationProducesAnUndeclaredKind(t *testing.T) {
	d := Describe()
	for _, op := range Operations {
		for _, k := range op.Produces {
			if _, ok := d.ArtifactSchemas[k]; !ok {
				t.Errorf("operation %q produces kind %q, which is not declared "+
					"in artifact_schemas; the reference resolves to nothing",
					op.ID, k)
			}
		}
	}
}
