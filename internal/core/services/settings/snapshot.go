package settings

import "time"

// Source says where a setting's value came from.
type Source string

const (
	SourceDefault     Source = "default"
	SourceDatabase    Source = "database"
	SourceEnvironment Source = "environment"
)

// Snapshot is the settings at one moment. It never changes: a change makes a new one.
type Snapshot struct {
	values  map[string]any
	sources map[string]Source
}

func (s *Snapshot) value(key string) any {
	v, ok := s.values[key]
	if !ok {
		// Keys are constants in the code: an unknown one is a bug, caught by tests.
		panic("settings: unknown setting " + key)
	}

	return v
}

func (s *Snapshot) String(key string) string          { return s.value(key).(string) }
func (s *Snapshot) Int(key string) int                { return s.value(key).(int) }
func (s *Snapshot) Bool(key string) bool              { return s.value(key).(bool) }
func (s *Snapshot) Duration(key string) time.Duration { return s.value(key).(time.Duration) }
func (s *Snapshot) Strings(key string) []string       { return s.value(key).([]string) }

func (s *Snapshot) Source(key string) Source {
	return s.sources[key]
}
