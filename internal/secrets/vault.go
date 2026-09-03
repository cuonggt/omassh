// Package secrets stores identity secrets outside the Omassh database.
//
// Nothing in here is ever written to the bbolt store: the store holds identity
// records, and the secret material for each one lives in the OS keychain under
// the identity's id.
package secrets

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound reports that no secret is stored for an id.
var ErrNotFound = errors.New("no secret stored")

// Vault stores one secret per identity id.
type Vault interface {
	Set(id, secret string) error
	Get(id string) (string, error)
	Delete(id string) error
	Kind() string
}

// Open returns the vault named by kind. "memory" exists so tests and scripted
// runs never touch the user's real keychain.
func Open(kind, service string) (Vault, error) {
	switch kind {
	case "", "keyring":
		return Keyring{service: service}, nil
	case "memory":
		return NewMemory(), nil
	default:
		return nil, errors.New("unknown secret store: " + kind)
	}
}

// Keyring is the OS credential store: Keychain on macOS, Secret Service on
// Linux, Credential Manager on Windows.
type Keyring struct{ service string }

func (k Keyring) Kind() string { return "keyring" }

func (k Keyring) Set(id, secret string) error {
	return keyring.Set(k.service, id, secret)
}

func (k Keyring) Get(id string) (string, error) {
	s, err := keyring.Get(k.service, id)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return s, err
}

func (k Keyring) Delete(id string) error {
	err := keyring.Delete(k.service, id)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil // deleting what isn't there is not a failure
	}
	return err
}

// Memory is a process-lifetime vault used by tests and by --secrets=memory.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (v *Memory) Kind() string { return "memory" }

func (v *Memory) Set(id, secret string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.m[id] = secret
	return nil
}

func (v *Memory) Get(id string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.m[id]
	if !ok {
		return "", ErrNotFound
	}
	return s, nil
}

func (v *Memory) Delete(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.m, id)
	return nil
}
