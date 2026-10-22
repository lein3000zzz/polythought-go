package plugindir_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jedisct1/go-minisign"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/plugindir"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

// signer is a minisign key the tests sign with.
type signer struct {
	sk  minisign.PrivateKey
	pub string // the public key's line, as its .pub file has it
}

func newSigner(t *testing.T) signer {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	var sk minisign.PrivateKey
	sk.SignatureAlgorithm = [2]byte{'E', 'd'}
	_, _ = rand.Read(sk.KeyId[:])
	copy(sk.SecretKey[:], priv)

	line := base64.StdEncoding.EncodeToString(append(append([]byte("Ed"), sk.KeyId[:]...), pub...))

	return signer{sk: sk, pub: line}
}

func (s signer) trusted(t *testing.T, name string) []plugindir.TrustedKey {
	t.Helper()

	keys, err := plugindir.ParseTrustedKeys([]string{name + " " + s.pub})
	require.NoError(t, err)

	return keys
}

func manifest(version string) []byte {
	raw, _ := json.Marshal(plugin.Manifest{
		ID: "weather", Version: version, Name: "Weather", SDK: plugin.SDKVersion,
		Nodes: []plugin.NodeType{{Name: "forecast"}},
	})

	return raw
}

// files are a good archive's, for this server's platform and another.
func files(version string) map[string][]byte {
	return map[string][]byte{
		"manifest.json": manifest(version),
		"bin/" + plugindir.Platform() + "/plugin": []byte("#!/bin/sh\necho program\n"),
		"bin/" + other() + "/plugin":              []byte("another platform's"),
		"LICENSE":                                 []byte("MIT"),
	}
}

func other() string {
	if plugindir.Platform() == "linux_arm64" {
		return "linux_amd64"
	}

	return "linux_arm64"
}

// archive packs files with their checksums, signed by by when it is given; tweak may
// change the checksums' text before it is signed.
func archive(t *testing.T, fs map[string][]byte, by *signer, tweak func(sums string) string) []byte {
	t.Helper()

	var sums strings.Builder

	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range fs {
			if !yield(k) {
				return
			}
		}
	}) {
		h := sha256.Sum256(fs[name])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}

	all := map[string][]byte{}
	for k, v := range fs {
		all[k] = v
	}

	text := sums.String()
	if tweak != nil {
		text = tweak(text)
	}

	all["checksums.txt"] = []byte(text)

	if by != nil {
		sig, err := by.sk.Sign([]byte(text), minisign.SignOptions{Hashed: true, TrustedComment: "weather 1.0.0"})
		require.NoError(t, err)
		all["checksums.txt.minisig"] = sig.Encode()
	}

	return pack(t, all, nil)
}

// pack is a gzipped tar of files, with extra headers as they are.
func pack(t *testing.T, files map[string][]byte, extra []*tar.Header) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	}) {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(files[name])), Typeflag: tar.TypeReg}))
		_, err := tw.Write(files[name])
		require.NoError(t, err)
	}

	for _, h := range extra {
		require.NoError(t, tw.WriteHeader(h))
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	return buf.Bytes()
}

func open(t *testing.T) *plugindir.Dir {
	t.Helper()

	d, err := plugindir.Open(filepath.Join(t.TempDir(), "plugins"))
	require.NoError(t, err)

	return d
}

func TestInstall(t *testing.T) {
	alice := newSigner(t)

	t.Run("signed by a trusted key", func(t *testing.T) {
		d := open(t)

		v, err := d.Install(bytes.NewReader(archive(t, files("1.0.0"), &alice, nil)), 1<<20, alice.trusted(t, "Alice"))
		require.NoError(t, err)

		assert.Equal(t, "weather", v.Manifest.ID)
		assert.Equal(t, "1.0.0", v.Manifest.Version)
		assert.True(t, v.Signed)
		assert.Equal(t, "Alice", v.Signer)
		assert.Empty(t, v.Warning)
		assert.Equal(t, filepath.Join(d.Root(), "weather", "1.0.0", "bin", plugindir.Platform(), "plugin"), v.Program)

		info, err := os.Stat(v.Program)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "its program runnable by the server only")

		found, err := d.List()
		require.NoError(t, err)
		assert.Equal(t, []plugindir.Found{{ID: "weather", Version: "1.0.0"}}, found, "nothing of the unpacking left")

		_, err = d.Install(bytes.NewReader(archive(t, files("1.0.0"), &alice, nil)), 1<<20, nil)
		require.ErrorIs(t, err, plugindir.ErrInstalled)
	})

	t.Run("unsigned, or by a key not trusted: with a warning", func(t *testing.T) {
		d := open(t)

		v, err := d.Install(bytes.NewReader(archive(t, files("1.0.0"), nil, nil)), 1<<20, alice.trusted(t, "Alice"))
		require.NoError(t, err)
		assert.False(t, v.Signed)
		assert.Equal(t, "It is not signed: nobody vouches for what it is.", v.Warning)

		mallory := newSigner(t)
		v, err = d.Install(bytes.NewReader(archive(t, files("1.1.0"), &mallory, nil)), 1<<20, alice.trusted(t, "Alice"))
		require.NoError(t, err)
		assert.True(t, v.Signed)
		assert.Empty(t, v.Signer)
		assert.Contains(t, v.Warning, "signed by a key you do not trust")
	})

	t.Run("refused when anything is wrong", func(t *testing.T) {
		withFile := func(name string, body []byte) map[string][]byte {
			fs := files("1.0.0")
			fs[name] = body

			return fs
		}

		without := func(name string) map[string][]byte {
			fs := files("1.0.0")
			delete(fs, name)

			return fs
		}

		for name, raw := range map[string][]byte{
			"not gzipped":            []byte("plain"),
			"a file it may not hold": archive(t, withFile("run.sh", []byte("x")), nil, nil),
			"a path out":             archive(t, withFile("../escape", []byte("x")), nil, nil),
			"no manifest":            archive(t, without("manifest.json"), nil, nil),
			"no program for here":    archive(t, without("bin/"+plugindir.Platform()+"/plugin"), nil, nil),
			"a bad manifest":         archive(t, withFile("manifest.json", []byte(`{"id":"Bad"}`)), nil, nil),
			"a manifest with more":   archive(t, withFile("manifest.json", []byte(`{"id":"weather","extra":1}`)), nil, nil),
			"a checksum wrong": archive(t, files("1.0.0"), nil, func(s string) string {
				return strings.Replace(s, s[:4], "0000", 1)
			}),
			"a file not listed": archive(t, files("1.0.0"), nil, func(s string) string {
				lines := strings.SplitAfter(s, "\n")
				return strings.Join(lines[1:], "")
			}),
			"a link": pack(t, map[string][]byte{"manifest.json": manifest("1.0.0")}, []*tar.Header{
				{Name: "LICENSE", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink},
			}),
			"larger than it may be": archive(t, withFile("README.md", bytes.Repeat([]byte("x"), 2<<20)), nil, nil),
		} {
			d := open(t)

			_, err := d.Install(bytes.NewReader(raw), 1<<16, nil)
			require.Error(t, err, name)
			assert.ErrorIs(t, err, plugindir.ErrInvalid, name)

			found, _ := d.List()
			assert.Empty(t, found, name+": nothing kept")
		}

		d := open(t)
		forged := archive(t, files("1.0.0"), &alice, nil)
		// The same archive's checksums, signed by alice, then changed: her signature no longer holds.
		_, err := d.Install(bytes.NewReader(resign(t, forged, alice, func(s string) string { return s + "\n" })), 1<<20, alice.trusted(t, "Alice"))
		require.ErrorIs(t, err, plugindir.ErrInvalid, "as good as forged")
	})
}

// resign rewrites an archive's checksums after alice signed them, keeping her signature.
func resign(t *testing.T, raw []byte, _ signer, change func(string) string) []byte {
	t.Helper()

	gz, err := gzip.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)

	tr := tar.NewReader(gz)
	fs := map[string][]byte{}

	for {
		h, err := tr.Next()
		if err != nil {
			break
		}

		var b bytes.Buffer
		_, _ = b.ReadFrom(tr)
		fs[h.Name] = b.Bytes()
	}

	fs["checksums.txt"] = []byte(change(string(fs["checksums.txt"])))

	return pack(t, fs, nil)
}

func TestOpen(t *testing.T) {
	alice := newSigner(t)
	d := open(t)

	v, err := d.Install(bytes.NewReader(archive(t, files("1.0.0"), &alice, nil)), 1<<20, alice.trusted(t, "Alice"))
	require.NoError(t, err)

	t.Run("checked again as it is about to run", func(t *testing.T) {
		again, err := d.Open("weather", "1.0.0", alice.trusted(t, "Alice"))
		require.NoError(t, err)
		assert.Equal(t, v.Program, again.Program)
		assert.Equal(t, "Alice", again.Signer)

		_, err = d.Open("weather", "../weather", nil)
		require.ErrorIs(t, err, plugindir.ErrInvalid)
	})

	t.Run("a program changed on disk is caught", func(t *testing.T) {
		require.NoError(t, os.WriteFile(v.Program, []byte("#!/bin/sh\necho evil\n"), 0o700))

		_, err := d.Open("weather", "1.0.0", alice.trusted(t, "Alice"))
		require.ErrorIs(t, err, plugindir.ErrTampered)
	})

	t.Run("a file added on disk is caught", func(t *testing.T) {
		require.NoError(t, d.Remove("weather", "1.0.0"))
		_, err := d.Install(bytes.NewReader(archive(t, files("1.0.0"), &alice, nil)), 1<<20, alice.trusted(t, "Alice"))
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(d.Root(), "weather", "1.0.0", "README.md"), []byte("hi"), 0o600))

		_, err = d.Open("weather", "1.0.0", nil)
		require.ErrorIs(t, err, plugindir.ErrTampered)
	})

	t.Run("removed, and its plugin's directory once empty", func(t *testing.T) {
		require.NoError(t, d.Remove("weather", "1.0.0"))

		_, err := os.Stat(filepath.Join(d.Root(), "weather"))
		assert.True(t, os.IsNotExist(err))
	})
}

// A plugin's lasting directory is beside its versions, never taken for one, and kept as
// they go until it is removed (D339, D342).
func TestData(t *testing.T) {
	alice := newSigner(t)
	d := open(t)

	v, err := d.Install(bytes.NewReader(archive(t, files("1.0.0"), &alice, nil)), 1<<20, alice.trusted(t, "Alice"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(d.Root(), ".data", "weather"), v.Data)

	none, err := d.Data()
	require.NoError(t, err)
	assert.Empty(t, none, "made as it first runs")

	require.NoError(t, os.MkdirAll(v.Data, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(v.Data, "kept"), make([]byte, 5000), 0o600))

	found, err := d.List()
	require.NoError(t, err)
	assert.Equal(t, []plugindir.Found{{ID: "weather", Version: "1.0.0"}}, found, "no version")

	require.NoError(t, d.Remove("weather", "1.0.0"))

	data, err := d.Data()
	require.NoError(t, err)
	assert.Equal(t, []plugindir.Data{{ID: "weather", Bytes: 4096 + 8192}}, data, "kept as its versions go: a block for the directory, two for the file")

	require.NoError(t, d.RemoveData("weather"))

	data, err = d.Data()
	require.NoError(t, err)
	assert.Empty(t, data)

	require.ErrorIs(t, d.RemoveData("../weather"), plugindir.ErrInvalid)

	// By its ID as a manifest has it: where the filesystem ignores case, Weather would be weather's (P27).
	require.NoError(t, os.MkdirAll(v.Data, 0o700))
	require.ErrorIs(t, d.RemoveData("Weather"), plugindir.ErrInvalid)
	assert.DirExists(t, v.Data)

	require.NoError(t, os.MkdirAll(filepath.Join(d.Root(), ".data", "Other"), 0o700))
	data, err = d.Data()
	require.NoError(t, err)
	assert.Equal(t, []string{"weather"}, idsOf(data), "only what could be removed")
}

func idsOf(data []plugindir.Data) []string {
	out := make([]string, len(data))
	for i, d := range data {
		out[i] = d.ID
	}

	return out
}

func TestTrustedKeys(t *testing.T) {
	// minisign's own test key and signature, from its Go library's tests.
	keys, err := plugindir.ParseTrustedKeys([]string{"RWQf6LRCGA9i53mlYecO4IzT51TGPpvWucNSCh1CBM0QTaLn73Y7GFO3", " ", "Frank RWQf6LRCGA9i53mlYecO4IzT51TGPpvWucNSCh1CBM0QTaLn73Y7GFO3"})
	require.NoError(t, err)
	require.Len(t, keys, 2, "blank entries left out")
	assert.Equal(t, "E7620F1842B4E81F", keys[0].Name, "no name: its ID, as minisign prints it")
	assert.Equal(t, "Frank", keys[1].Name)

	sig, err := minisign.DecodeSignature("untrusted comment: signature from minisign secret key\nRUQf6LRCGA9i559r3g7V1qNyJDApGip8MfqcadIgT9CuhV3EMhHoN1mGTkUidF/z7SrlQgXdy8ofjb7bNJJylDOocrCo8KLzZwo=\ntrusted comment: timestamp:1635443258\tfile:test\thashed\n/cj37GK60vryibFn+ftOgbCvW9NKhKYgjVpFFQUcWPAnjO23wrvVDTt7cloNC06maoBli9q6qwZDXXoaxweICQ==\n")
	require.NoError(t, err)

	ok, err := keys[1].Key.Verify([]byte("test"), sig)
	require.NoError(t, err)
	assert.True(t, ok, "a signature minisign itself made")

	for _, bad := range []string{"RWQnot", "Frank", "untrusted comment: minisign public key"} {
		_, err := plugindir.ParseTrustedKeys([]string{bad})
		assert.Error(t, err, bad)
	}
}

// A manifest is kept, and read again at every start: one larger than 256 KiB is refused,
// unpacked or on disk, before it is read; and an archive unpacks to 4 times its bytes at
// most, whatever the cap (P5, D322).
func TestSizes(t *testing.T) {
	t.Run("a manifest too large, unpacked", func(t *testing.T) {
		fs := files("1.0.0")
		fs["manifest.json"] = append(manifest("1.0.0"), bytes.Repeat([]byte(" "), 256<<10)...)

		_, err := open(t).Install(bytes.NewReader(archive(t, fs, nil, nil)), 100<<20, nil)
		require.ErrorIs(t, err, plugindir.ErrInvalid)
		assert.Contains(t, err.Error(), "manifest.json is larger than the 262144 bytes it may be")
	})

	t.Run("a manifest too large, on disk", func(t *testing.T) {
		d := open(t)

		_, err := d.Install(bytes.NewReader(archive(t, files("1.0.0"), nil, nil)), 1<<20, nil)
		require.NoError(t, err)

		// Rewritten, with checksums.txt to match.
		at := filepath.Join(d.Root(), "weather", "1.0.0")
		fs := files("1.0.0")
		fs["manifest.json"] = append(manifest("1.0.0"), bytes.Repeat([]byte(" "), 256<<10)...)
		require.NoError(t, os.WriteFile(filepath.Join(at, "manifest.json"), fs["manifest.json"], 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(at, "checksums.txt"), []byte(sumsOf(fs)), 0o600))

		_, err = d.Open("weather", "1.0.0", nil)
		require.ErrorIs(t, err, plugindir.ErrInvalid)
		assert.Contains(t, err.Error(), "manifest.json is larger than")
	})

	t.Run("more than 4 times its bytes, under any cap", func(t *testing.T) {
		raw := archive(t, withFile("README.md", bytes.Repeat([]byte("x"), 8<<20)), nil, nil)
		require.Less(t, len(raw), 64<<10, "it compresses well")

		_, err := open(t).Install(bytes.NewReader(raw), 100<<20, nil)
		require.ErrorIs(t, err, plugindir.ErrInvalid)
		assert.Contains(t, err.Error(), "it unpacks to more than 4 times its size")
	})
}

func withFile(name string, body []byte) map[string][]byte {
	fs := files("1.0.0")
	fs[name] = body

	return fs
}

// sumsOf is checksums.txt for files.
func sumsOf(fs map[string][]byte) string {
	var sums strings.Builder

	for _, name := range slices.Sorted(maps.Keys(fs)) {
		h := sha256.Sum256(fs[name])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}

	return sums.String()
}

// A version's digest is its checksums.txt's, which holds every file's, its program's
// among them (P16); and its manifest is kept as decoded, one value whoever reads it (P19).
func TestDigest(t *testing.T) {
	d := open(t)

	fs := files("1.0.0")
	fs["manifest.json"] = []byte(" {\n  \"version\": \"1.0.0\", \"id\": \"weather\", \"name\": \"<Weather>\", \"sdk\": \"" + plugin.SDKVersion + "\",\n  \"nodes\": [{\"name\": \"forecast\"}] }\n")

	f, err := plugindir.NewFiles(d, nil).Install(bytes.NewReader(archive(t, fs, nil, nil)), 1<<20)
	require.NoError(t, err)

	sums := sha256.Sum256([]byte(sumsOf(fs)))
	program := sha256.Sum256(fs["bin/"+plugindir.Platform()+"/plugin"])

	assert.Equal(t, hex.EncodeToString(sums[:]), f.Digest)
	assert.Equal(t, hex.EncodeToString(program[:]), f.ProgramSum)

	want, err := json.Marshal(plugin.Manifest{
		ID: "weather", Version: "1.0.0", Name: "<Weather>", SDK: plugin.SDKVersion, Nodes: []plugin.NodeType{{Name: "forecast"}},
	})
	require.NoError(t, err)
	assert.Equal(t, string(want), string(f.Manifest))

	again, err := plugindir.NewFiles(d, nil).Open("weather", "1.0.0")
	require.NoError(t, err)
	assert.Equal(t, f.Digest, again.Digest)
}

// encoding/json takes keys equal ignoring case for one, the last winning: a manifest with
// such keys, anywhere in it, is refused, so what is shown is what runs (P19).
func TestManifestKeys(t *testing.T) {
	base := `"id":"weather","version":"1.0.0","sdk":"` + plugin.SDKVersion + `"`

	for name, m := range map[string]string{
		"nodes, then Nodes": `{` + base + `,"name":"Weather","nodes":[{"name":"forecast"}],"Nodes":[{"name":"buyEverything","trades":true}]}`,
		"a name twice":      `{` + base + `,"name":"Weather","name":"Polythought"}`,
		"in a node":         `{` + base + `,"name":"Weather","nodes":[{"name":"forecast","label":"A","LABEL":"B"}]}`,
		// The Kelvin sign folds to k, as encoding/json matches it.
		"by a fold": `{` + base + `,"name":"Weather","nodes":[{"name":"forecast","params":[{"name":"p","kind":"string","\u212aind":"number"}]}]}`,
		"in a scenario's nodes": `{` + base + `,"name":"Weather","scenarios":[{"id":"s","name":"S","summary":"S",` +
			`"nodes":[{"id":"a","ID":"b"}],"connections":[]}]}`,
		"two values": `{` + base + `,"name":"Weather"} {}`,
	} {
		fs := files("1.0.0")
		fs["manifest.json"] = []byte(m)

		_, err := open(t).Install(bytes.NewReader(archive(t, fs, nil, nil)), 1<<20, nil)
		require.ErrorIs(t, err, plugindir.ErrInvalid, name)
	}

	fs := files("1.0.0")
	fs["manifest.json"] = []byte(`{` + base + `,"name":"Weather","nodes":[{"name":"forecast","outputs":["main"],"params":[{"name":"name","kind":"string"}]}]}`)
	_, err := open(t).Install(bytes.NewReader(archive(t, fs, nil, nil)), 1<<20, nil)
	require.NoError(t, err, "the same key in objects of their own")
}

// A version that cannot name its directory is refused before it is put there, so nothing of
// it is left (P20); and what an install left unpacked as the server stopped is removed as
// the directory is opened.
func TestNames(t *testing.T) {
	for _, version := range []string{"1.0.0-a..b", "1.0.0-" + strings.Repeat("a", 120)} {
		d := open(t)

		for range 2 {
			_, err := d.Install(bytes.NewReader(archive(t, files(version), nil, nil)), 1<<20, nil)
			require.ErrorIs(t, err, plugindir.ErrInvalid, version)
			require.NotErrorIs(t, err, plugindir.ErrInstalled, version)
		}

		left, err := os.ReadDir(d.Root())
		require.NoError(t, err)
		assert.Empty(t, left, version)
	}

	root := filepath.Join(t.TempDir(), "plugins")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".incoming-123", "bin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".incoming-123", "manifest.json"), []byte("{}"), 0o600))

	_, err := plugindir.Open(root)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(root, ".incoming-123"))
}
