package main

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	copilotauth "github.com/xibodev/llm-provider-auth/copilot"
)

var startDevice = copilotauth.StartDeviceFlow
var pollDevice = copilotauth.PollDeviceFlowTokenOnce

type LoginStatus struct {
	ID              string    `json:"id"`
	UserCode        string    `json:"userCode"`
	VerificationURI string    `json:"verificationURI"`
	Interval        int       `json:"interval"`
	ExpiresAt       time.Time `json:"expiresAt"`
}
type loginFlow struct {
	LoginStatus
	model, deviceCode, authorizedToken string
	generation                         uint64
	nextPoll                           time.Time
	mu                                 sync.Mutex
}
type LoginResult struct {
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Interval int    `json:"interval,omitempty"`
}

func (a *App) StartLogin(model string) (LoginStatus, error) {
	model = strings.TrimSpace(model)
	if len(model) > 200 {
		return LoginStatus{}, fmt.Errorf("model id is too long")
	}
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return LoginStatus{}, fmt.Errorf("stop the active turn before signing in")
	}
	a.authGeneration++
	generation := a.authGeneration
	a.login = nil
	a.mu.Unlock()
	code, err := startDevice()
	if err != nil {
		return LoginStatus{}, err
	}
	u, err := url.Parse(code.VerificationURI)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil {
		return LoginStatus{}, fmt.Errorf("unexpected device authorization URL")
	}
	if code.DeviceCode == "" || code.UserCode == "" || code.ExpiresIn <= 0 {
		return LoginStatus{}, fmt.Errorf("device authorization response is incomplete")
	}
	flow := &loginFlow{LoginStatus: LoginStatus{ID: randomID(), UserCode: code.UserCode, VerificationURI: code.VerificationURI, Interval: max(5, code.Interval), ExpiresAt: time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)}, model: model, deviceCode: code.DeviceCode, generation: generation}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.authGeneration != generation {
		return LoginStatus{}, fmt.Errorf("sign-in request was superseded")
	}
	a.login = flow
	return flow.LoginStatus, nil
}
func (a *App) PollLogin(id string) (LoginResult, error) {
	a.mu.Lock()
	flow := a.login
	a.mu.Unlock()
	if flow == nil || flow.ID != id {
		return LoginResult{}, fmt.Errorf("sign-in request is unavailable")
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.authorizedToken == "" {
		if time.Now().After(flow.ExpiresAt) {
			return LoginResult{Status: "failed", Error: "Device sign-in expired; start again"}, nil
		}
		if time.Now().Before(flow.nextPoll) {
			return LoginResult{Status: "pending", Interval: flow.Interval}, nil
		}
		result := pollDevice(flow.deviceCode)
		flow.nextPoll = time.Now().Add(time.Duration(flow.Interval) * time.Second)
		switch result.Status {
		case "pending":
			return LoginResult{Status: "pending", Interval: flow.Interval}, nil
		case "slow_down":
			flow.Interval += 5
			return LoginResult{Status: "pending", Interval: flow.Interval}, nil
		case "authorized":
			if result.AccessToken == "" {
				return LoginResult{Status: "failed", Error: "Device authorization returned no credential"}, nil
			}
			flow.authorizedToken = result.AccessToken
		default:
			return LoginResult{Status: "failed", Error: result.Error}, nil
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.login != flow || a.authGeneration != flow.generation {
		return LoginResult{}, fmt.Errorf("sign-in request was superseded")
	}
	if a.active != nil {
		return LoginResult{Status: "pending", Interval: flow.Interval, Error: "Sign-in authorized; waiting for the active turn to finish."}, nil
	}
	if err := a.applyModelLocked(ModelInput{Provider: "github-copilot", Model: flow.model, APIKey: flow.authorizedToken}); err != nil {
		return LoginResult{}, err
	}
	a.authGeneration++
	a.login = nil
	flow.authorizedToken = ""
	return LoginResult{Status: "success"}, nil
}
