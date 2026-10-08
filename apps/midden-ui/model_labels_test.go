package main

import (
	"strings"
	"testing"

	"github.com/xibodev/llmgw-core/providers"
)

func TestProvidersWithoutADefaultAddressAskForOne(t *testing.T) {
	without := 0
	for _, item := range providerRoster() {
		if strings.TrimSpace(item.DefaultEndpoint) != "" {
			continue
		}
		without++
		if !item.RequiresBaseURL {
			t.Errorf("%s has no default address, but the Models page would call it optional", item.ID)
		}
	}
	if without == 0 {
		t.Fatal("the roster has no provider without a default address, such as Amazon Bedrock, to check")
	}
}

func TestFreeServicesAreNamedAsInTheProviderList(t *testing.T) {
	roster := rosterByID()
	checked := 0
	for _, profile := range providers.AnonymousProviderProfiles() {
		item, ok := roster[profile.RegistryID]
		if !ok || profile.ProviderID == "" || profile.ProviderID == profile.RegistryID {
			continue
		}
		if got := freeLabel(profile.ProviderID, "", roster); got != item.Label {
			t.Errorf("%s is named %q, want %q", profile.ProviderID, got, item.Label)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no free service has a connection id unlike its registry id, as kilo-code and kilo_code are")
	}
	if got := freeLabel("synthetic-unknown", "", roster); got != "synthetic-unknown" {
		t.Fatalf("an unknown service is named %q", got)
	}
}
