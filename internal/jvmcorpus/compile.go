package jvmcorpus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samibel/graphi/internal/jvmgroundtruth"
)

// # Staging, and why the oracle needs it
//
// The two sides of the differential name source files differently. graphi names
// them by their path under the ingest root. javap names them by the SourceFile
// attribute, which carries only the FILE NAME — the package supplies the rest,
// so a class reconstructs to `com/google/common/base/Ascii.java` and nothing
// else. Point graphi at a pin's natural layout and every path disagrees
// (`guava/src/com/google/common/base/Ascii.java` vs the above), so every fact
// scores external and the whole run is a vacuous green.
//
// Staging fixes this by construction rather than by a fixup table: each source
// root is stripped to its package-relative path and the roots are OVERLAID into
// one tree, which is exactly the shape javap reconstructs. graphi then ingests
// the same tree javac/kotlinc compiled, and the two sides agree because they are
// looking at the same paths.
//
// # Collisions are excluded and COUNTED, never resolved by picking one
//
// An overlay can collide: Kotlin-multiplatform expect/actual pairs put
// `okio/Buffer.kt` in commonMain AND in jvmMain, and javap's SourceFile
// attribute cannot tell them apart — both classes claim the same source path.
// Silently keeping one would make the oracle attribute the other's calls to the
// survivor, which is a fabricated fact of exactly the kind this programme
// treats as stop-ship. So a collision EXCLUDES every file in it and is counted
// into the published denominator, where a reader can see how much of the pin the
// figure does not cover.

// StageReport is the file-level denominator of one pin (AC-4): what exists at
// the pin, what the strategy offered, and what was dropped and why.
type StageReport struct {
	// FilesAtPin is every JVM source file tracked at the pin, whatever its
	// target. It is the honest outer denominator: a JVM-target coverage figure
	// that quietly uses the JVM source set as its denominator reads as covering
	// the repository when it covers one target of it.
	FilesAtPin int `json:"files_at_pin"`
	// FilesOffered is what the strategy's source roots select.
	FilesOffered int `json:"files_offered"`
	// FilesStaged is what actually reached the compiler.
	FilesStaged int `json:"files_staged"`
	// CollidingPaths are the package-relative paths claimed by more than one
	// offered file, sorted. FilesDroppedToCollision counts the FILES, which is
	// the larger number and the one that belongs in a coverage statement.
	CollidingPaths          []string `json:"colliding_paths,omitempty"`
	FilesDroppedToCollision int      `json:"files_dropped_to_collision"`
	// Staged is every staged path, sorted; Common is the subset that came from
	// a CommonSourceRoots directory. Both are relative to the staging root, and
	// both are sorted, because the compiler's argument order is a build input.
	Staged []string `json:"-"`
	Common []string `json:"-"`
}

// Stage overlays the strategy's source roots into stagedRoot at their
// package-relative paths, excluding and counting collisions.
//
// filesAtPin is supplied by the caller (from `git ls-files` at the pin) rather
// than walked here, so the outer denominator is the PIN's census and not
// whatever happens to be on disk in a dirty checkout.
func Stage(pinRoot, stagedRoot string, s *Strategy, filesAtPin int) (StageReport, error) {
	rep := StageReport{FilesAtPin: filesAtPin}

	common := map[string]bool{}
	for _, r := range s.CommonSourceRoots {
		common[r] = true
	}

	// Collect (packageRelativePath -> absolute source paths).
	claims := map[string][]string{}
	fromCommon := map[string]bool{}
	for _, root := range s.SourceRoots {
		isCommon := common[root]
		abs := filepath.Join(pinRoot, filepath.FromSlash(root))
		err := filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !isJVMSource(p) {
				return nil
			}
			rel, rerr := filepath.Rel(abs, p)
			if rerr != nil {
				return rerr
			}
			key := filepath.ToSlash(rel)
			claims[key] = append(claims[key], p)
			if isCommon {
				fromCommon[key] = true
			}
			return nil
		})
		if err != nil {
			return rep, fmt.Errorf("jvmcorpus: walk source root %q: %w", root, err)
		}
	}

	keys := make([]string, 0, len(claims))
	for k := range claims {
		keys = append(keys, k)
		rep.FilesOffered += len(claims[k])
	}
	sort.Strings(keys)

	for _, k := range keys {
		srcs := claims[k]
		if len(srcs) > 1 {
			// A collision. Drop every file in it and count them.
			rep.CollidingPaths = append(rep.CollidingPaths, k)
			rep.FilesDroppedToCollision += len(srcs)
			continue
		}
		dst := filepath.Join(stagedRoot, filepath.FromSlash(k))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return rep, err
		}
		if err := copyFile(srcs[0], dst); err != nil {
			return rep, err
		}
		rep.FilesStaged++
		rep.Staged = append(rep.Staged, k)
		if fromCommon[k] {
			rep.Common = append(rep.Common, k)
		}
	}
	return rep, nil
}

// Compile runs the pin's compiler over the staged sources and returns the
// output directory's class count. It is a thin, explicit wrapper: every flag
// comes from the strategy data, nothing is added silently, and the full command
// is returned so a report can print exactly what ran.
//
// Two flags ARE added here, and both are about reproducibility rather than
// about the pin:
//
//   - the source list is the SORTED staged list, so argument order is a
//     function of the sources and not of a directory walk;
//   - javac is forced into the C locale (-J-Duser.language=en -J-Duser.country=US).
//     This is not cosmetic. javac localises its diagnostics, and the sandbox
//     this strategy was developed in emits them in German ("6960 Fehler"), so
//     any run that counts or classifies compiler output is environment-dependent
//     until the locale is pinned. That is a reproducibility defect that would
//     have surfaced only as a mysterious difference between two CI runners.
func Compile(compilerPath, stagedRoot, outDir string, s *Strategy, rep StageReport, classpath, plugins []string) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	var args []string
	if s.Compiler == "javac" {
		args = append(args, "-J-Duser.language=en", "-J-Duser.country=US")
	}
	args = append(args, s.CompilerArgs...)
	for _, p := range plugins {
		args = append(args, "-Xplugin="+p)
	}
	if len(s.CommonSourceRoots) > 0 && len(rep.Common) > 0 {
		args = append(args, "-Xcommon-sources="+strings.Join(rep.Common, ","))
	}
	if len(classpath) > 0 {
		args = append(args, "-classpath", strings.Join(classpath, string(os.PathListSeparator)))
	}
	args = append(args, "-d", outDir)
	args = append(args, rep.Staged...)

	cmd := exec.Command(compilerPath, args...)
	cmd.Dir = stagedRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return args, fmt.Errorf("jvmcorpus: %s failed: %w\n%s", s.Compiler, err, tail(out, 4000))
	}
	return args, nil
}

func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return "…" + string(b[len(b)-n:])
}

func isJVMSource(p string) bool {
	return strings.HasSuffix(p, ".java") || strings.HasSuffix(p, ".kt")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// StagedSources lists the staged source files, sorted, as paths relative to
// stagedRoot. Sorted because the compiler's argument order is an input to the
// build and an unsorted directory walk would make it environment-dependent —
// the exact class of non-determinism AC-5 exists to catch.
func StagedSources(stagedRoot string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(stagedRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isJVMSource(p) {
			return nil
		}
		rel, rerr := filepath.Rel(stagedRoot, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// VerifyArtifacts checks every pinned artifact's sha256 against the file of the
// same base name in libDir, and returns them split by role: classpath entries
// and compiler plugins, each in manifest order.
//
// Fail-closed by design: a digest mismatch aborts rather than warning. A compile
// against unexpected bytes is not the compile the strategy describes, and a
// figure produced by it would be attributed to a pin it did not use. This is
// also the answer to "is the toolchain pinned END TO END, including transitively
// fetched compiler plugins?" — the plugin is verified here by the same rule as
// everything else, before it can influence a single emitted class.
func VerifyArtifacts(libDir string, s *Strategy) (classpath, plugins []string, err error) {
	for _, a := range s.Classpath {
		name := path.Base(a.URL)
		p := filepath.Join(libDir, name)
		f, ferr := os.Open(p)
		if ferr != nil {
			return nil, nil, fmt.Errorf("jvmcorpus: pinned artifact %s not found at %s: %w", a.Coordinate, p, ferr)
		}
		h := sha256.New()
		_, cerr := io.Copy(h, f)
		f.Close()
		if cerr != nil {
			return nil, nil, cerr
		}
		got := hex.EncodeToString(h.Sum(nil))
		if got != a.SHA256 {
			return nil, nil, fmt.Errorf("jvmcorpus: pinned artifact %s DIGEST MISMATCH at %s:\n  want %s\n  got  %s\n"+
				"the compile would run against bytes the strategy does not describe, so it is refused",
				a.Coordinate, p, a.SHA256, got)
		}
		if a.Role == RoleCompilerPlugin {
			plugins = append(plugins, p)
		} else {
			classpath = append(classpath, p)
		}
	}
	return classpath, plugins, nil
}

// CaptureResult is the bytecode-side denominator of one pin.
type CaptureResult struct {
	// ClassesOnDisk is what the compiler produced — the REQUIRED set the
	// completeness gate is checked against.
	ClassesOnDisk int `json:"classes_on_disk"`
	// ClassesCaptured is what the merged capture contains. The gate guarantees
	// it is a superset of the required set, so these are equal on success; both
	// are reported because a reader should not have to trust that.
	ClassesCaptured int `json:"classes_captured"`
	// Shards is how many javap execs the capture took.
	Shards int `json:"shards"`
	// Digest is the sha256 of the exact bytes the oracle consumed (AC-5).
	Digest string `json:"digest"`
	// Bytes is the merged capture size.
	Bytes int `json:"bytes"`
}

// CompiledClasses enumerates every `.class` under outDir as a dotted name,
// sorted. This is the REQUIRED set: it comes from the compiler's own output
// directory, never from the capture, because a required set derived from the
// capture is satisfied exactly when it should fail.
func CompiledClasses(outDir string) ([]string, error) {
	var classes []string
	err := filepath.WalkDir(outDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".class") {
			return err
		}
		rel, rerr := filepath.Rel(outDir, p)
		if rerr != nil {
			return rerr
		}
		cls := strings.TrimSuffix(filepath.ToSlash(rel), ".class")
		classes = append(classes, strings.ReplaceAll(cls, "/", "."))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(classes)
	return classes, nil
}

// DefaultShardBytes is the per-exec argument budget for javap. It is far below
// any real ARG_MAX (Linux ~2 MB, macOS 1 MB) precisely so the SHARDED path is
// the one that runs on every corpus, large or small — a safety mechanism that
// only engages on the biggest input is a safety mechanism that is never tested.
const DefaultShardBytes = 32 * 1024

// Capture disassembles every compiled class and returns a capture that has
// PASSED the completeness gate.
//
// The gate is not optional and not conditional on size. Sharding is always on,
// so the merge-and-verify path is exercised by every run rather than only by the
// run that first exceeds an argument limit — which would be the run that
// discovers the forge in production.
func Capture(javap, outDir string, classes []string) (*jvmgroundtruth.Capture, CaptureResult, error) {
	res := CaptureResult{ClassesOnDisk: len(classes)}
	batches := jvmgroundtruth.ShardClasses(classes, DefaultShardBytes)
	res.Shards = len(batches)

	var shards [][]byte
	for _, b := range batches {
		args := append([]string{"-c", "-p", "-s", "-classpath", outDir}, b...)
		out, err := exec.Command(javap, args...).Output()
		if err != nil {
			return nil, res, fmt.Errorf("jvmcorpus: javap shard (%d classes): %w", len(b), err)
		}
		shards = append(shards, out)
	}

	capt, err := jvmgroundtruth.NewCapture(shards, classes)
	if err != nil {
		// This is the forged-stop-ship path being refused. Surface it as-is:
		// the error names the missing classes, which is what a diagnosis needs.
		return nil, res, err
	}
	res.ClassesCaptured = len(capt.Classes())
	res.Digest = capt.Digest()
	res.Bytes = len(capt.Bytes())
	return capt, res, nil
}

// SourceBytes reads the staged sources into the map shape the binder consumes.
func SourceBytes(stagedRoot string, rels []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(rels))
	for _, rel := range rels {
		b, err := os.ReadFile(filepath.Join(stagedRoot, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		out[rel] = b
	}
	return out, nil
}
