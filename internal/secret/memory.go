package secret

import "sync"

// Memory keeps passwords for as long as the process runs.
//
// It is what the rest of omassh is tested against. A test that reached the
// real keychain would write into the keychain of whoever ran it — the same
// objection the tmux tests answer with a socket of their own — and on a
// machine with no keyring there would be nothing to reach at all.
func Memory() Store { return &memory{} }

type memory struct {
	mu sync.Mutex
	by map[string]string
}

func (m *memory) Get(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.by[id]
	if !ok {
		return "", ErrNotFound
	}
	return p, nil
}

func (m *memory) Set(id, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.by == nil {
		m.by = map[string]string{}
	}
	m.by[id] = password
	return nil
}

func (m *memory) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.by, id)
	return nil
}
