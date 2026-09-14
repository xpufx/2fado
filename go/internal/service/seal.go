// Artifact sealing: TOCTOU mitigation that bundles the petition's
// mutable inputs into an immutable tar.gz at T0 with a SHA-256 manifest,
// binds approval to the manifest root hash, and executes strictly
// against the frozen copies at T1. Any live mutation aborts with a
// seal-drift block; verified runs rewrite file argv tokens to the
// sealed copies so attacker bytes never execute.
//
// Scope is direct inputs only: the entrypoint plus cwd-relative argv
// tokens that resolve to existing regular files. Ambient relative
// reads inside scripts stay covered by the git-tree drift check (#37);
// full filesystem freezing is the namespace job (#39). Everything
// degrades gracefully: unsealable petitions yield nil and run normally.
package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"2fado/internal/protocol"
)

const (
	maxSealFiles    = 32
	maxSealTotal    = 8 << 20
	maxSealFileSize = 2 << 20
)

// sealStateDir resolves the on-disk home for sealed artifacts.
func (s Service) sealStateDir() string {
	if s.Store.Dir != "" {
		return s.Store.Dir
	}
	return s.Conf.StateDir
}

func sealArchivePath(stateDir, rid string) string {
	return filepath.Join(stateDir, "sealed", rid+".tar.gz")
}

func sealExtractDir(stateDir, rid string) string {
	return filepath.Join(stateDir, "sealed", rid+".d")
}

// sealTarget pairs an argv token's live source with its archive name.
type sealTarget struct {
	abs string
	rel string
}

// sealCandidates collects mutable workspace inputs for argv in cwd:
// tokens resolving to regular files inside cwd (the entrypoint script,
// relative file arguments, or absolute paths under cwd). Flags,
// stdio-looking tokens, missing paths, and files outside cwd are
// skipped: the entrypoint binary outside cwd is the fd pin's job (#36).
// Results are deduped, Rel-sorted, and capped so petitions stay cheap.
func sealCandidates(argv []string, cwd string) []sealTarget {
	if len(argv) == 0 || strings.TrimSpace(cwd) == "" {
		return nil
	}
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return nil
	}
	seen := map[string]bool{}
	var out []sealTarget
	for _, tok := range argv {
		if strings.TrimSpace(tok) == "" || strings.HasPrefix(tok, "-") {
			continue
		}
		if strings.ContainsAny(tok, "\x00\n|&;<>(){}!$`\"'") {
			continue
		}
		if strings.Contains(tok, "://") || strings.Contains(tok, "=") {
			continue
		}
		clean := filepath.Clean(tok)
		var abs string
		if filepath.IsAbs(clean) {
			abs = clean
		} else {
			abs = filepath.Join(cwd, clean)
		}
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() || !st.Mode().IsRegular() {
			continue
		}
		rel, err := filepath.Rel(cwd, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, sealTarget{abs: abs, rel: rel})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	if len(out) > maxSealFiles {
		out = out[:maxSealFiles]
	}
	return out
}

func hashSealBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sealRoot folds sorted rel+hash pairs into the manifest root hash that
// approval binds to.
func sealRoot(files []protocol.SealFile) string {
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.Rel + "\x00" + f.SHA256 + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// snapshotSeal bundles argv's mutable inputs into an immutable tar.gz
// for rid and returns its manifest. It returns nil when there is
// nothing sealable or anything fails, so callers run unsealed.
func (s Service) snapshotSeal(rid string, argv []string, cwd string) *protocol.SealPin {
	stateDir := s.sealStateDir()
	if strings.TrimSpace(rid) == "" || stateDir == "" {
		return nil
	}
	targets := sealCandidates(argv, cwd)
	if len(targets) == 0 {
		return nil
	}
	type blob struct {
		t sealTarget
		b []byte
		m os.FileMode
	}
	var blobs []blob
	var total int64
	for _, t := range targets {
		st, err := os.Stat(t.abs)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		if st.Size() > maxSealFileSize {
			continue
		}
		b, err := os.ReadFile(t.abs)
		if err != nil || int64(len(b)) > maxSealFileSize {
			continue
		}
		total += int64(len(b))
		if total > maxSealTotal {
			break
		}
		blobs = append(blobs, blob{t: t, b: b, m: st.Mode()})
	}
	if len(blobs) == 0 {
		return nil
	}
	files := make([]protocol.SealFile, 0, len(blobs))
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, bl := range blobs {
		files = append(files, protocol.SealFile{
			Rel:    bl.t.rel,
			Abs:    bl.t.abs,
			SHA256: hashSealBytes(bl.b),
			Size:   int64(len(bl.b)),
			Mode:   uint32(bl.m),
		})
		if err := tw.WriteHeader(&tar.Header{
			Name: bl.t.rel, Mode: 0o600, Size: int64(len(bl.b)),
		}); err != nil {
			gz.Close()
			return nil
		}
		if _, err := tw.Write(bl.b); err != nil {
			gz.Close()
			return nil
		}
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		return nil
	}
	if err := gz.Close(); err != nil {
		return nil
	}
	archive := sealArchivePath(stateDir, rid)
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		return nil
	}
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		return nil
	}
	return &protocol.SealPin{
		Root:    sealRoot(files),
		Files:   files,
		Archive: archive,
	}
}

// checkSealDrift compares the T0 manifest stored under rid against the
// live inputs. It returns nil when there is nothing to check so
// execution proceeds.
func (s Service) checkSealDrift(rid string) *protocol.SealDrift {
	rec, err := s.Store.Load(rid)
	if err != nil || rec.Seal == nil || len(rec.Seal.Files) == 0 {
		return nil
	}
	for _, f := range rec.Seal.Files {
		if f.Abs == "" || f.Rel == "" {
			continue
		}
		st, err := os.Stat(f.Abs)
		if err != nil {
			return &protocol.SealDrift{Drift: true, Block: true,
				Reason: "sealed input " + f.Rel + " is gone at approval"}
		}
		if !st.Mode().IsRegular() {
			return &protocol.SealDrift{Drift: true, Block: true,
				Reason: "sealed input " + f.Rel + " changed type since petition"}
		}
		if st.Size() != f.Size {
			return &protocol.SealDrift{Drift: true, Block: true,
				Reason: "sealed input " + f.Rel + " changed since petition (" + shortHash(f.SHA256) + " -> size " + itoaSize(st.Size()) + ")"}
		}
		b, err := os.ReadFile(f.Abs)
		if err != nil {
			return &protocol.SealDrift{Drift: true, Block: true,
				Reason: "sealed input " + f.Rel + " is unreadable at approval"}
		}
		if live := hashSealBytes(b); live != f.SHA256 {
			return &protocol.SealDrift{Drift: true, Block: true,
				Reason: "sealed input " + f.Rel + " changed since petition (" + shortHash(f.SHA256) + " -> " + shortHash(live) + ")"}
		}
	}
	return nil
}

func itoaSize(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// materializeSeal verifies the archive against the manifest and
// extracts the frozen copies for rid. Callers must have passed
// checkSealDrift first; any error here must block, never run live.
func (s Service) materializeSeal(rid string) (string, error) {
	rec, err := s.Store.Load(rid)
	if err != nil || rec.Seal == nil || len(rec.Seal.Files) == 0 {
		return "", io.ErrUnexpectedEOF
	}
	pin := rec.Seal
	want := map[string]protocol.SealFile{}
	for _, f := range pin.Files {
		want[f.Rel] = f
	}
	archive := pin.Archive
	if archive == "" {
		archive = sealArchivePath(s.sealStateDir(), rid)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		return "", err
	}
	dir := sealExtractDir(s.sealStateDir(), rid)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		want_file, ok := want[hdr.Name]
		if !ok || seen[hdr.Name] || hdr.Size > maxSealFileSize {
			_ = os.RemoveAll(dir)
			return "", io.ErrUnexpectedEOF
		}
		seen[hdr.Name] = true
		b, err := io.ReadAll(io.LimitReader(tr, maxSealFileSize+1))
		if err != nil || int64(len(b)) != hdr.Size {
			_ = os.RemoveAll(dir)
			return "", io.ErrUnexpectedEOF
		}
		if hashSealBytes(b) != want_file.SHA256 {
			_ = os.RemoveAll(dir)
			return "", io.ErrUnexpectedEOF
		}
		dst := filepath.Join(dir, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		perm := os.FileMode(0o600 | os.FileMode(want_file.Mode&0o111))
		if err := os.WriteFile(dst, b, perm); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	if len(seen) != len(want) {
		_ = os.RemoveAll(dir)
		return "", io.ErrUnexpectedEOF
	}
	return dir, nil
}

// sealedArgv rewrites argv tokens naming sealed inputs to their frozen
// copies under dir. Unsealed tokens pass through untouched.
func sealedArgv(argv []string, cwd string, pin *protocol.SealPin, dir string) []string {
	if pin == nil || len(pin.Files) == 0 {
		return argv
	}
	byAbs := map[string]string{}
	for _, f := range pin.Files {
		byAbs[f.Abs] = filepath.Join(dir, filepath.FromSlash(f.Rel))
	}
	out := append([]string{}, argv...)
	for i, tok := range argv {
		if strings.TrimSpace(tok) == "" {
			continue
		}
		clean := filepath.Clean(tok)
		var abs string
		if filepath.IsAbs(clean) {
			abs = clean
		} else if strings.TrimSpace(cwd) != "" {
			abs = filepath.Join(cwd, clean)
		}
		if sealed, ok := byAbs[abs]; ok {
			out[i] = sealed
		}
	}
	return out
}
