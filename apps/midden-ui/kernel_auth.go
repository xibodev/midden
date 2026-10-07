package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xibodev/llm-provider-auth/tokenstore"
	core "github.com/xibodev/llmgw-core"
)

// Keys of the kernel's auth store that Midden writes.
const (
	// extensionDaemonKey holds the extension service's shared secret.
	extensionDaemonKey = "extension-daemon"
	// apiKeyAuthMethod marks a stored API key.
	apiKeyAuthMethod = "api_key"
)

// authCredential is one credential in the kernel's auth.json, in Compa's
// format.
type authCredential struct {
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	AccountID    string            `json:"account_id,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at,omitempty"`
	Provider     string            `json:"provider"`
	AuthMethod   string            `json:"auth_method"`
	IDToken      string            `json:"id_token,omitempty"`
	TokenType    string            `json:"token_type,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	// Revision changes on every token store write; it fences out writers
	// whose view is stale.
	Revision string `json:"revision,omitempty"`
}

func (c *authCredential) expired() bool {
	return !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt)
}

// authStore is the kernel's auth.json. Credentials Midden does not change are
// kept exactly as read.
type authStore struct{ path string }

func kernelAuth(home string) authStore { return authStore{filepath.Join(home, "auth.json")} }

func authKey(key string) string { return strings.ToLower(strings.TrimSpace(key)) }

func (s authStore) read() (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read stored keys: %w", err)
	}
	var file struct {
		Credentials map[string]json.RawMessage `json:"credentials"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read stored keys: %w", err)
	}
	if file.Credentials == nil {
		file.Credentials = map[string]json.RawMessage{}
	}
	return file.Credentials, nil
}

// get returns the credential stored under key, or nil.
func (s authStore) get(key string) (*authCredential, error) {
	credentials, err := s.read()
	if err != nil {
		return nil, err
	}
	raw, ok := credentials[authKey(key)]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var credential authCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return nil, fmt.Errorf("read stored key %q: %w", key, err)
	}
	return &credential, nil
}

// update changes the stored credentials under the lock; change reports
// whether it changed anything to save.
func (s authStore) update(change func(map[string]json.RawMessage) (bool, error)) error {
	return withFileLock(s.path, func() error {
		credentials, err := s.read()
		if err != nil {
			return err
		}
		changed, err := change(credentials)
		if err != nil || !changed {
			return err
		}
		data, err := json.MarshalIndent(map[string]any{"credentials": credentials}, "", "  ")
		if err != nil {
			return err
		}
		return replaceFile(s.path, data)
	})
}

// set stores credential under key; nil removes it.
func (s authStore) set(key string, credential *authCredential) error {
	if credential == nil {
		return s.remove(key)
	}
	stored := *credential
	if stored.Provider = authKey(stored.Provider); stored.Provider == "" {
		stored.Provider = authKey(key)
	}
	raw, err := json.Marshal(&stored)
	if err != nil {
		return err
	}
	return s.update(func(credentials map[string]json.RawMessage) (bool, error) {
		credentials[authKey(key)] = raw
		return true, nil
	})
}

// remove deletes the credentials stored under keys.
func (s authStore) remove(keys ...string) error {
	return s.update(func(credentials map[string]json.RawMessage) (bool, error) {
		changed := false
		for _, key := range keys {
			if _, ok := credentials[authKey(key)]; ok {
				delete(credentials, authKey(key))
				changed = true
			}
		}
		return changed, nil
	})
}

// resolveCredentialRef returns the secret an instance's auth_connection_ref
// names: credential:<key>, an unexpired access token in the auth store.
func resolveCredentialRef(home, ref string) (string, error) {
	kind, key, found := strings.Cut(strings.TrimSpace(ref), ":")
	key = strings.TrimSpace(key)
	if !found || kind != "credential" || key == "" {
		return "", errors.New("auth_connection_ref must use credential:<store-key>")
	}
	credential, err := kernelAuth(home).get(key)
	if err != nil {
		return "", err
	}
	if credential == nil || strings.TrimSpace(credential.AccessToken) == "" {
		return "", fmt.Errorf("credential %q has no access token", key)
	}
	if credential.expired() {
		return "", fmt.Errorf("credential %q is expired", key)
	}
	return strings.TrimSpace(credential.AccessToken), nil
}

// authTokenStore is the token store over auth.json that signed-in credentials
// live in, shared with the kernel: writes replace a credential only while its
// revision is current, and a refresh lease is an OS lock on a file beside
// auth.json, so a refresh token is spent once.
type authTokenStore struct{ auth authStore }

var (
	_ tokenstore.Store     = authTokenStore{}
	_ core.CredentialStore = authTokenStore{}
)

func newRevision() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("tokenstore: revision: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func recordOf(credential *authCredential) tokenstore.Record {
	return tokenstore.Record{Revision: credential.Revision, AccessToken: credential.AccessToken, RefreshToken: credential.RefreshToken,
		IDToken: credential.IDToken, TokenType: credential.TokenType, Expiry: credential.ExpiresAt, AccountID: credential.AccountID,
		Metadata: cloneStrings(credential.Metadata)}
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func checkTokenKey(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("tokenstore: key is required")
	}
	return ctx.Err()
}

// Resolve names no credential: an instance names its sign-in in its settings.
func (authTokenStore) Resolve(context.Context, core.Caller, string) (string, error) {
	return "", core.ErrNoCredential
}

func (s authTokenStore) Load(ctx context.Context, key string) (tokenstore.Record, error) {
	if err := checkTokenKey(ctx, key); err != nil {
		return tokenstore.Record{}, err
	}
	credential, err := s.auth.get(key)
	if err != nil {
		return tokenstore.Record{}, err
	}
	if credential == nil {
		return tokenstore.Record{}, tokenstore.ErrNotFound
	}
	return recordOf(credential), nil
}

func (s authTokenStore) write(key string, record tokenstore.Record, allowed func(*authCredential) error) (tokenstore.Record, error) {
	revision, err := newRevision()
	if err != nil {
		return tokenstore.Record{}, err
	}
	record = record.Clone()
	stored := &authCredential{AccessToken: record.AccessToken, RefreshToken: record.RefreshToken, IDToken: record.IDToken,
		TokenType: record.TokenType, ExpiresAt: record.Expiry, AccountID: record.AccountID, Metadata: record.Metadata,
		Provider: authKey(key), AuthMethod: "oauth", Revision: revision}
	raw, err := json.Marshal(stored)
	if err != nil {
		return tokenstore.Record{}, err
	}
	err = s.auth.update(func(credentials map[string]json.RawMessage) (bool, error) {
		var current *authCredential
		if existing, ok := credentials[authKey(key)]; ok && string(bytes.TrimSpace(existing)) != "null" {
			current = &authCredential{}
			if err := json.Unmarshal(existing, current); err != nil {
				return false, err
			}
		}
		if err := allowed(current); err != nil {
			return false, err
		}
		credentials[authKey(key)] = raw
		return true, nil
	})
	if err != nil {
		return tokenstore.Record{}, err
	}
	return recordOf(stored), nil
}

// Save stores record unconditionally, as a new sign-in does.
func (s authTokenStore) Save(ctx context.Context, key string, record tokenstore.Record) (tokenstore.Record, error) {
	if err := checkTokenKey(ctx, key); err != nil {
		return tokenstore.Record{}, err
	}
	return s.write(key, record, func(*authCredential) error { return nil })
}

// ReplaceIfCurrent stores record only while the stored revision is revision.
func (s authTokenStore) ReplaceIfCurrent(ctx context.Context, key, revision string, record tokenstore.Record) (tokenstore.Record, error) {
	if err := checkTokenKey(ctx, key); err != nil {
		return tokenstore.Record{}, err
	}
	return s.write(key, record, func(current *authCredential) error {
		if current == nil || current.Revision == "" || current.Revision != revision {
			return tokenstore.ErrConflict
		}
		return nil
	})
}

// RevokeIfCurrent removes the credential only while its revision is revision.
func (s authTokenStore) RevokeIfCurrent(ctx context.Context, key, revision string) error {
	if err := checkTokenKey(ctx, key); err != nil {
		return err
	}
	return s.auth.update(func(credentials map[string]json.RawMessage) (bool, error) {
		raw, ok := credentials[authKey(key)]
		if !ok {
			return false, tokenstore.ErrNotFound
		}
		var current authCredential
		if err := json.Unmarshal(raw, &current); err != nil {
			return false, err
		}
		if current.Revision == "" || current.Revision != revision {
			return false, tokenstore.ErrConflict
		}
		delete(credentials, authKey(key))
		return true, nil
	})
}

// leaseSlots serializes leases in this process; some platforms grant OS locks
// per process.
var leaseSlots sync.Map // lease path -> chan struct{}

// Lease blocks until it holds the refresh lease for key or ctx ends.
func (s authTokenStore) Lease(ctx context.Context, key string) (func(), error) {
	if err := checkTokenKey(ctx, key); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(authKey(key)))
	path := s.auth.path + ".lease-" + hex.EncodeToString(sum[:8])
	slot, _ := leaseSlots.LoadOrStore(path, make(chan struct{}, 1))
	local := slot.(chan struct{})
	select {
	case local <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	releaseLocal := func() { <-local }
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		releaseLocal()
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		releaseLocal()
		return nil, fmt.Errorf("tokenstore: open the lease: %w", err)
	}
	for {
		held, err := tryLockFile(file)
		if err != nil {
			file.Close()
			releaseLocal()
			return nil, fmt.Errorf("tokenstore: take the lease: %w", err)
		}
		if held {
			break
		}
		select {
		case <-ctx.Done():
			file.Close()
			releaseLocal()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = unlockFile(file)
			_ = file.Close()
			releaseLocal()
		})
	}, nil
}
