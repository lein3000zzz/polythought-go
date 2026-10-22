// Package plugindir keeps installed plugins in a directory on disk (D315), each version
// as its archive unpacked: <dir>/<id>/<version>/.
//
// Each plugin also has a lasting directory of its own, <dir>/.data/<id>/, which its process
// may write (D339): kept as its versions come and go.
//
// An archive is a gzipped tar of exactly these files:
//
//	manifest.json              the plugin's manifest
//	bin/<os>_<arch>/plugin     its program for each platform it runs on, linux_amd64 say
//	checksums.txt              the SHA-256 of every other file, as sha256sum writes them
//	checksums.txt.minisig      optional: a minisign signature of checksums.txt (D316)
//	LICENSE, README.md         optional
//
// Nothing else, and no links: an archive that has more is refused. Every file is
// checked against checksums.txt, and its signature against the keys an admin trusts, as
// it is installed and again each time it is opened to run, so a file changed on disk is
// caught.
package plugindir

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/jedisct1/go-minisign"

	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

const (
	manifestFile  = "manifest.json"
	checksumsFile = "checksums.txt"
	signatureFile = "checksums.txt.minisig"
	programName   = "plugin"
	dataDir       = ".data" // hidden, so never taken for a plugin

	maxFiles = 64
	// unpackedPer is how much larger than its archive an archive may unpack to: more is
	// a bomb.
	unpackedPer = 4
)

// platforms are those an archive may carry a program for.
var platforms = []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"}

// Platform is this server's, as archives name it.
func Platform() string { return runtime.GOOS + "_" + runtime.GOARCH }

var (
	// ErrInvalid is an archive, or a version on disk, that is not a plugin as this
	// package keeps them: the error says why.
	ErrInvalid = errors.New("not a plugin archive")

	// ErrInstalled is a version installed already.
	ErrInstalled = errors.New("that version is installed already")

	// ErrTampered is a version whose files no longer match their checksums or
	// signature.
	ErrTampered = errors.New("its files do not match what was installed")
)

// Dir is the directory plugins are kept in.
type Dir struct {
	root string
}

// Open opens the directory at root, making it if it is not there.
func Open(root string) (*Dir, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("plugins directory: %w", err)
	}

	return &Dir{root: root}, nil
}

// Root is where the directory is.
func (d *Dir) Root() string { return d.root }

// Version is a version of a plugin as the directory has it, checked.
type Version struct {
	Manifest plugin.Manifest
	Path     string // its directory
	Program  string // its program for this server's platform
	Data     string // its plugin's lasting directory, which may not be made yet

	// Signed says it has a signature; Signer is the name of the trusted key that made
	// it, empty when the key is none an admin trusts, which Warning then says.
	Signed  bool
	Signer  string
	KeyID   string // the signing key's ID, as minisign shows it
	Warning string // for the admin who approves it, when it is not signed by a trusted key
}

// Install unpacks an archive of at most max bytes and checks it, keeping it as the
// version its manifest says. The archive is refused whole when anything is wrong.
func (d *Dir) Install(archive io.Reader, max int64, keys []TrustedKey) (Version, error) {
	incoming, err := os.MkdirTemp(d.root, ".incoming-")
	if err != nil {
		return Version{}, fmt.Errorf("plugins directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(incoming) }()

	if err := unpack(archive, max, incoming); err != nil {
		return Version{}, err
	}

	v, err := check(incoming, keys, ErrInvalid)
	if err != nil {
		return Version{}, err
	}

	dir := filepath.Join(d.root, v.Manifest.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Version{}, fmt.Errorf("plugins directory: %w", err)
	}

	to := filepath.Join(dir, v.Manifest.Version)
	if _, err := os.Lstat(to); err == nil {
		return Version{}, fmt.Errorf("%s %s: %w", v.Manifest.ID, v.Manifest.Version, ErrInstalled)
	}

	if err := os.Rename(incoming, to); err != nil {
		return Version{}, fmt.Errorf("plugins directory: %w", err)
	}

	return d.Open(v.Manifest.ID, v.Manifest.Version, keys)
}

// Open checks a version on disk again, as it is about to run: ErrTampered when its files
// no longer match.
func (d *Dir) Open(id, version string, keys []TrustedKey) (Version, error) {
	if !safeName(id) || !safeName(version) {
		return Version{}, fmt.Errorf("%w: %q %q", ErrInvalid, id, version)
	}

	at := filepath.Join(d.root, id, version)

	v, err := check(at, keys, ErrTampered)
	if err != nil {
		return Version{}, err
	}

	if v.Manifest.ID != id || v.Manifest.Version != version {
		return Version{}, fmt.Errorf("%w: %s holds %s %s", ErrTampered, at, v.Manifest.ID, v.Manifest.Version)
	}

	v.Data = filepath.Join(d.root, dataDir, id)

	return v, nil
}

// Remove deletes a version from disk, and its plugin's directory once it holds none.
func (d *Dir) Remove(id, version string) error {
	if !safeName(id) || !safeName(version) {
		return fmt.Errorf("%w: %q %q", ErrInvalid, id, version)
	}

	if err := os.RemoveAll(filepath.Join(d.root, id, version)); err != nil {
		return err
	}

	left, err := os.ReadDir(filepath.Join(d.root, id))
	if err == nil && len(left) == 0 {
		_ = os.Remove(filepath.Join(d.root, id))
	}

	return nil
}

// Found is a version on disk.
type Found struct {
	ID, Version string
}

// List is every version on disk, by plugin and version: what an admin installed, and
// what someone copied there (D321).
func (d *Dir) List() ([]Found, error) {
	plugins, err := os.ReadDir(d.root)
	if err != nil {
		return nil, err
	}

	var out []Found

	for _, p := range plugins {
		if !p.IsDir() || !safeName(p.Name()) {
			continue // .incoming- and the like
		}

		versions, err := os.ReadDir(filepath.Join(d.root, p.Name()))
		if err != nil {
			return nil, err
		}

		for _, v := range versions {
			if v.IsDir() && safeName(v.Name()) {
				out = append(out, Found{ID: p.Name(), Version: v.Name()})
			}
		}
	}

	return out, nil
}

// safeName is a directory name the package makes: never a path, nor hidden.
var safeNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,99}$`)

func safeName(s string) bool {
	return safeNameRE.MatchString(s) && s != "." && !strings.Contains(s, "..")
}

// allowed says whether an archive may hold a file of that name.
func allowed(name string) bool {
	switch name {
	case manifestFile, checksumsFile, signatureFile, "LICENSE", "README.md":
		return true
	}

	plat, ok := strings.CutPrefix(name, "bin/")
	plat, ok2 := strings.CutSuffix(plat, "/"+programName)

	return ok && ok2 && slices.Contains(platforms, plat)
}

// allowedDir says whether an archive may hold a directory of that name.
func allowedDir(name string) bool {
	plat, ok := strings.CutPrefix(name, "bin/")
	return name == "bin" || (ok && slices.Contains(platforms, plat))
}

// unpack writes an archive's files under to, refusing what it may not hold: links, files
// it does not name, more than it may unpack to.
func unpack(archive io.Reader, max int64, to string) error {
	limited := &io.LimitedReader{R: archive, N: max + 1}

	gz, err := gzip.NewReader(limited)
	if err != nil {
		return fmt.Errorf("%w: it is not gzipped: %v", ErrInvalid, err)
	}

	tr := tar.NewReader(gz)
	budget := max * unpackedPer
	seen := map[string]bool{}

	for {
		h, err := tr.Next()

		switch {
		case errors.Is(err, io.EOF):
			if limited.N <= 0 {
				return fmt.Errorf("%w: it is larger than the %d bytes an archive may be", ErrInvalid, max)
			}

			return nil
		case limited.N <= 0:
			return fmt.Errorf("%w: it is larger than the %d bytes an archive may be", ErrInvalid, max)
		case err != nil:
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}

		name := path.Clean(strings.TrimPrefix(h.Name, "./"))

		switch {
		case seen[name]:
			return fmt.Errorf("%w: %s twice", ErrInvalid, name)
		case len(seen) >= maxFiles:
			return fmt.Errorf("%w: more than %d files", ErrInvalid, maxFiles)
		case h.Typeflag == tar.TypeDir && allowedDir(name):
			seen[name] = true
			if err := os.MkdirAll(filepath.Join(to, filepath.FromSlash(name)), 0o700); err != nil {
				return err
			}

			continue
		case h.Typeflag != tar.TypeReg || !allowed(name):
			return fmt.Errorf("%w: %s is none of the files a plugin archive holds", ErrInvalid, h.Name)
		case h.Size > budget:
			return fmt.Errorf("%w: it unpacks to more than %d bytes", ErrInvalid, max*unpackedPer)
		}

		seen[name] = true
		budget -= h.Size

		if err := write(filepath.Join(to, filepath.FromSlash(name)), tr, h.Size, strings.HasSuffix(name, "/"+programName)); err != nil {
			return err
		}
	}
}

func write(at string, r io.Reader, size int64, program bool) error {
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		return err
	}

	mode := os.FileMode(0o600)
	if program {
		mode = 0o700
	}

	f, err := os.OpenFile(at, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}

	if _, err := io.CopyN(f, r, size); err != nil {
		_ = f.Close()
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	return f.Close()
}

// check checks a version's files: only those an archive may hold, each matching
// checksums.txt and listed there, its signature by a trusted key when it has one, its
// manifest one the host takes, and a program for this platform. What does not match is
// mismatch: an archive not as it claims, or files changed since it was installed.
func check(dir string, keys []TrustedKey, mismatch error) (Version, error) {
	files, err := filesIn(dir, mismatch)
	if err != nil {
		return Version{}, err
	}

	sums, err := os.ReadFile(filepath.Join(dir, checksumsFile))
	if err != nil {
		return Version{}, fmt.Errorf("%w: no %s", ErrInvalid, checksumsFile)
	}

	listed, err := parseChecksums(sums)
	if err != nil {
		return Version{}, err
	}

	delete(files, checksumsFile)
	delete(files, signatureFile)

	if !slices.Equal(slices.Sorted(maps.Keys(files)), slices.Sorted(maps.Keys(listed))) {
		return Version{}, fmt.Errorf("%w: %s lists other files than it holds", mismatch, checksumsFile)
	}

	for name, want := range listed {
		got, err := sumOf(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return Version{}, err
		}

		if got != want {
			return Version{}, fmt.Errorf("%w: %s does not match its checksum", mismatch, name)
		}
	}

	v := Version{Path: dir, Program: filepath.Join(dir, "bin", Platform(), programName)}

	if err := v.signature(filepath.Join(dir, signatureFile), sums, keys, mismatch); err != nil {
		return Version{}, err
	}

	raw, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return Version{}, fmt.Errorf("%w: no %s", ErrInvalid, manifestFile)
	}

	if v.Manifest, err = decodeManifest(raw); err != nil {
		return Version{}, err
	}

	if _, ok := files["bin/"+Platform()+"/"+programName]; !ok {
		return Version{}, fmt.Errorf("%w: it has no program for %s, this server's platform", ErrInvalid, Platform())
	}

	return v, nil
}

// signature checks the version's signature, if it has one: by a key an admin trusts it
// must hold, or the version is not what it claims; by any other it is as good as none.
func (v *Version) signature(at string, sums []byte, keys []TrustedKey, mismatch error) error {
	raw, err := os.ReadFile(at)
	if errors.Is(err, fs.ErrNotExist) {
		v.Warning = "It is not signed: nobody vouches for what it is."
		return nil
	}

	if err != nil {
		return err
	}

	sig, err := minisign.DecodeSignature(string(raw))
	if err != nil {
		return fmt.Errorf("%w: its signature: %v", ErrInvalid, err)
	}

	v.Signed, v.KeyID = true, keyID(sig.KeyId)

	for _, k := range keys {
		if k.Key.KeyId != sig.KeyId {
			continue
		}

		if ok, err := k.Key.Verify(sums, sig); !ok {
			return fmt.Errorf("%w: its signature by %s does not hold: %v", mismatch, k.Name, err)
		}

		v.Signer = k.Name

		return nil
	}

	v.Warning = "It is signed by a key you do not trust (" + v.KeyID + "): nobody you trust vouches for what it is."

	return nil
}

// manifestJSON is a checked version's manifest.json, as it is.
func manifestJSON(dir string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, manifestFile)) //nolint:gosec // a file of the plugins directory
}

// decodeManifest reads manifest.json as the host reads Describe's, held to the same
// checks.
func decodeManifest(raw []byte) (plugin.Manifest, error) {
	var m plugin.Manifest

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()

	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("%w: %s: %v", ErrInvalid, manifestFile, err)
	}

	if err := pluginhost.Check(m); err != nil {
		return m, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	return m, nil
}

// filesIn are the files under dir, by their names in the archive: refused when one is a
// link or none an archive may hold.
func filesIn(dir string, mismatch error) (map[string]bool, error) {
	files := map[string]bool{}

	err := filepath.WalkDir(dir, func(at string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, at)
		if err != nil || rel == "." {
			return err
		}

		name := filepath.ToSlash(rel)

		switch {
		case e.IsDir() && allowedDir(name):
			return nil
		case !e.Type().IsRegular() || !allowed(name):
			return fmt.Errorf("%w: %s is none of the files a plugin holds", mismatch, name)
		}

		files[name] = true

		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s is not there", ErrInvalid, dir)
	}

	return files, err
}

// parseChecksums reads sha256sum's lines: a hash, two spaces (or a space and a star),
// a file's name.
func parseChecksums(raw []byte) (map[string]string, error) {
	out := map[string]string{}

	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}

		sum, name, ok := strings.Cut(line, " ")
		name = strings.TrimPrefix(strings.TrimPrefix(name, " "), "*")
		name = strings.TrimPrefix(name, "./")

		if _, err := hex.DecodeString(sum); !ok || len(sum) != 64 || err != nil || !allowed(name) || out[name] != "" {
			return nil, fmt.Errorf("%w: %s: %q is not a checksum of a file it may hold", ErrInvalid, checksumsFile, line)
		}

		out[name] = strings.ToLower(sum)
	}

	return out, sc.Err()
}

func sumOf(at string) (string, error) {
	f, err := os.Open(at) //nolint:gosec // a file of the plugins directory
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
