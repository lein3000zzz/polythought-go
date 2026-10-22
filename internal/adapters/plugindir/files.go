package plugindir

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.PluginFiles = (*Files)(nil)

// Files is the directory as the plugins service keeps versions in it, checked against
// the keys an admin trusts as they are now.
type Files struct {
	dir  *Dir
	keys func() []string // the trusted keys' entries, as the Config page lists them (D316)
}

func NewFiles(dir *Dir, keys func() []string) *Files {
	return &Files{dir: dir, keys: keys}
}

func (f *Files) trusted() ([]TrustedKey, error) {
	if f.keys == nil {
		return nil, nil
	}

	keys, err := ParseTrustedKeys(f.keys())
	if err != nil {
		return nil, fmt.Errorf("%w: the trusted keys: %v", domain.ErrInvalidInput, err)
	}

	return keys, nil
}

func (f *Files) Install(archive io.Reader, max int64) (ports.PluginFile, error) {
	keys, err := f.trusted()
	if err != nil {
		return ports.PluginFile{}, err
	}

	v, err := f.dir.Install(archive, max, keys)
	if err != nil {
		return ports.PluginFile{}, domainError(err)
	}

	return fileOf(v)
}

func (f *Files) Open(id, version string) (ports.PluginFile, error) {
	keys, err := f.trusted()
	if err != nil {
		return ports.PluginFile{}, err
	}

	v, err := f.dir.Open(id, version, keys)
	if err != nil {
		return ports.PluginFile{}, domainError(err)
	}

	return fileOf(v)
}

func (f *Files) Remove(id, version string) error { return domainError(f.dir.Remove(id, version)) }

func (f *Files) Data() ([]ports.PluginData, error) {
	data, err := f.dir.Data()
	if err != nil {
		return nil, err
	}

	out := make([]ports.PluginData, len(data))
	for i, d := range data {
		out[i] = ports.PluginData{ID: d.ID, Bytes: d.Bytes}
	}

	return out, nil
}

func (f *Files) RemoveData(id string) error { return domainError(f.dir.RemoveData(id)) }

func (f *Files) List() ([]ports.PluginFound, error) {
	found, err := f.dir.List()
	if err != nil {
		return nil, err
	}

	out := make([]ports.PluginFound, len(found))
	for i, v := range found {
		out[i] = ports.PluginFound{ID: v.ID, Version: v.Version}
	}

	return out, nil
}

func fileOf(v Version) (ports.PluginFile, error) {
	// The manifest as decoded, never its bytes: what is kept and shown is what runs (P19).
	raw, err := json.Marshal(v.Manifest)
	if err != nil {
		return ports.PluginFile{}, err
	}

	return ports.PluginFile{
		ID: v.Manifest.ID, Version: v.Manifest.Version, Name: v.Manifest.Name, Permissions: v.Manifest.Permissions,
		Manifest: raw, Program: v.Program, ProgramSum: v.ProgramSum, Dir: v.Path, Data: v.Data, Digest: v.Digest,
		Signed: v.Signed, Signer: v.Signer, KeyID: v.KeyID, Warning: v.Warning,
	}, nil
}

// domainError is the directory's error as the service tells it: an archive or files not
// as they should be are the admin's to fix.
func domainError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInstalled):
		return fmt.Errorf("%w: %v", domain.ErrPluginInstalled, err)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrTampered):
		return fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	default:
		return err
	}
}
