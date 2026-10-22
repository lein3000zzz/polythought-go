// Pluginpack packs a Go plugin as the archive admins install (D322): its program for each
// platform asked, manifest.json as the program describes itself, checksums.txt, and a
// minisign signature when given a key.
//
//	go run ./tools/pluginpack -o weather-1.0.0.tar.gz -platforms linux_amd64,linux_arm64 ./plugins/weather
//
// The program is built with CGO off, so one archive runs on any machine of a platform.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/jedisct1/go-minisign"
)

func main() {
	out := flag.String("o", "", "the archive to write, a .tar.gz")
	platforms := flag.String("platforms", runtime.GOOS+"_"+runtime.GOARCH, "the platforms to build for, os_arch, comma-separated")
	key := flag.String("sign", "", "a minisign secret key file to sign it with; its password, if it has one, in MINISIGN_PASSWORD")
	flag.Parse()

	if *out == "" || flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: pluginpack -o archive.tar.gz [-platforms linux_amd64,…] [-sign key] ./path/to/plugin")
		os.Exit(2)
	}

	if err := pack(flag.Arg(0), *out, strings.Split(*platforms, ","), *key); err != nil {
		fmt.Fprintln(os.Stderr, "pluginpack:", err)
		os.Exit(1)
	}
}

func pack(pkg, out string, platforms []string, keyFile string) error {
	work, err := os.MkdirTemp("", "pluginpack-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	// The manifest is the program's, as it describes itself: built for here, it says.
	here := filepath.Join(work, "here")
	if err := build(pkg, here, runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}

	manifest, err := exec.Command(here, "manifest").Output() //nolint:gosec // the plugin just built
	if err != nil {
		return fmt.Errorf("its manifest: %w", err)
	}

	files := map[string][]byte{"manifest.json": manifest}

	for _, plat := range platforms {
		goos, goarch, ok := strings.Cut(strings.TrimSpace(plat), "_")
		if !ok {
			return fmt.Errorf("platform %q: os_arch, like linux_amd64", plat)
		}

		bin := filepath.Join(work, plat)
		if err := build(pkg, bin, goos, goarch); err != nil {
			return err
		}

		program, err := os.ReadFile(bin) //nolint:gosec // the plugin just built
		if err != nil {
			return err
		}

		files["bin/"+goos+"_"+goarch+"/plugin"] = program
	}

	names := slices.Sorted(func(yield func(string) bool) {
		for name := range files {
			if !yield(name) {
				return
			}
		}
	})

	var sums bytes.Buffer
	for _, name := range names {
		h := sha256.Sum256(files[name])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}

	files["checksums.txt"] = sums.Bytes()

	if keyFile != "" {
		sig, err := sign(keyFile, sums.Bytes())
		if err != nil {
			return err
		}

		files["checksums.txt.minisig"] = sig
	}

	return write(out, files)
}

func build(pkg, out, goos, goarch string) error {
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, pkg) //nolint:gosec // a package the packer was given
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build for %s_%s: %w", goos, goarch, err)
	}

	return nil
}

func sign(keyFile string, sums []byte) ([]byte, error) {
	sk, err := minisign.NewPrivateKeyFromFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("the key: %w", err)
	}

	if sk.IsEncrypted() {
		password := os.Getenv("MINISIGN_PASSWORD")
		if password == "" {
			return nil, errors.New("the key has a password: give it in MINISIGN_PASSWORD")
		}

		if err := sk.Decrypt(password); err != nil {
			return nil, fmt.Errorf("the key: %w", err)
		}
	}

	sig, err := sk.Sign(sums, minisign.SignOptions{Hashed: true})
	if err != nil {
		return nil, err
	}

	return sig.Encode(), nil
}

// write packs files as a gzipped tar, in order, the programs runnable.
func write(out string, files map[string][]byte) error {
	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range files {
			if !yield(n) {
				return
			}
		}
	}) {
		mode := int64(0o644)
		if strings.HasSuffix(name, "/plugin") {
			mode = 0o755
		}

		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
			return err
		}

		if _, err := tw.Write(files[name]); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return err
	}

	if err := gz.Close(); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // where the archive goes
		return err
	}

	return os.WriteFile(out, buf.Bytes(), 0o644) //nolint:gosec // an archive to share
}
