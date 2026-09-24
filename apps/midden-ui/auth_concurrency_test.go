package main

import (
	"context"
	"testing"

	copilotauth "github.com/xibodev/llm-provider-auth/copilot"
)

func mockDeviceFlow(t *testing.T) {
	t.Helper()
	oldStart, oldPoll := startDevice, pollDevice
	t.Cleanup(func() { startDevice = oldStart; pollDevice = oldPoll })
	startDevice = func() (*copilotauth.DeviceCode, error) {
		return &copilotauth.DeviceCode{DeviceCode: "synthetic-device", UserCode: "CODE", VerificationURI: "https://github.com/login/device", ExpiresIn: 900, Interval: 5}, nil
	}
}
func TestLateSignInCannotReplaceAnExplicitNewerModel(t *testing.T) {
	mockDeviceFlow(t)
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	flow, err := app.StartLogin("first-model")
	if err != nil {
		t.Fatal(err)
	}
	pollDevice = func(string) copilotauth.DevicePollResult {
		return copilotauth.DevicePollResult{Status: "authorized", AccessToken: "synthetic-token"}
	}
	if err = app.SetModel(ModelInput{Provider: "openai", Model: "newer-model", Endpoint: "http://127.0.0.1:12345"}); err != nil {
		t.Fatal(err)
	}
	app.PollLogin(flow.ID)
	if app.model.Model != "newer-model" {
		t.Fatal("late sign-in replaced newer model choice")
	}
}
func TestAuthorizedTokenSurvivesATurnStartingDuringPoll(t *testing.T) {
	mockDeviceFlow(t)
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	flow, err := app.StartLogin("signed-in-model")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	pollDevice = func(string) copilotauth.DevicePollResult {
		calls++
		_, cancel := context.WithCancel(context.Background())
		app.mu.Lock()
		app.active = &activeTurn{ID: "busy", cancel: cancel}
		app.mu.Unlock()
		return copilotauth.DevicePollResult{Status: "authorized", AccessToken: "synthetic-token"}
	}
	app.PollLogin(flow.ID)
	app.mu.Lock()
	app.active.cancel()
	app.active = nil
	app.mu.Unlock()
	result, err := app.PollLogin(flow.ID)
	if err != nil || result.Status != "success" || calls != 1 {
		t.Fatalf("authorized token lost: %+v err=%v exchanges=%d", result, err, calls)
	}
}
