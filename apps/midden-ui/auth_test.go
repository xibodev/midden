package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xibodev/facet-studio/pkg/auth"
	copilotauth "github.com/xibodev/llm-provider-auth/copilot"
)

func TestDeviceSignInKeepsOAuthSecretsServerSide(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	oldStart, oldPoll := startDevice, pollDevice
	t.Cleanup(func() { startDevice = oldStart; pollDevice = oldPoll })
	startDevice = func() (*copilotauth.DeviceCode, error) {
		return &copilotauth.DeviceCode{DeviceCode: "synthetic-secret-device", UserCode: "ABCD-EFGH", VerificationURI: "https://github.com/login/device", Interval: 5, ExpiresIn: 900}, nil
	}
	pollDevice = func(code string) copilotauth.DevicePollResult {
		if code != "synthetic-secret-device" {
			t.Fatal("wrong device code")
		}
		return copilotauth.DevicePollResult{Status: "authorized", AccessToken: "synthetic-secret-access"}
	}
	flow, err := app.StartLogin("fixture-model")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(flow)
	if strings.Contains(string(raw), "synthetic-secret") {
		t.Fatal("device secret was returned to browser")
	}
	result, err := app.PollLogin(flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("login result=%+v", result)
	}
	stored, err := auth.GetCredential(app.model.CredentialRef)
	if err != nil || stored == nil || stored.AccessToken != "synthetic-secret-access" {
		t.Fatal("token not stored in isolated kernel auth", err)
	}
	raw, _ = json.Marshal(app.Status())
	if strings.Contains(string(raw), "synthetic-secret") {
		t.Fatal("access token leaked through status")
	}
}
