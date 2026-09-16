package theme

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// fileExt is the extension a theme file carries; the name is the stem.
const fileExt = ".theme"

// builtinFS holds the themes shipped in the binary.
//
//go:embed themes/*.theme
var builtinFS embed.FS

// ErrUnknown is returned by Lookup for a name no theme file answers to.
var ErrUnknown = errors.New("theme: unknown theme")

// Registry is every theme available to one invocation: the builtins, shadowed
// by any user file of the same name. It is read-only once built.
type Registry struct {
	// specs holds every theme that parsed.
	specs map[string]Spec

	// broken holds the parse error of every user file that did not. A broken
	// file removes a builtin it would have shadowed, so selecting that name is
	// the file's error rather than a silent fallback to the builtin.
	broken map[string]error

	// warnings describes each broken file, for the launcher to print.
	warnings []string
}

// Builtins is the registry of embedded themes alone. It is parsed once per
// process and shared, so callers must treat it as read-only.
func Builtins() Registry {
	return builtins()
}

// builtins parses the embedded themes once. A broken embedded file panics,
// which builtin_test.go makes unreachable.
var builtins = sync.OnceValue(func() Registry {
	entries, err := fs.ReadDir(builtinFS, "themes")
	if err != nil {
		panic(fmt.Sprintf("theme: reading embedded themes: %v", err))
	}

	reg := Registry{specs: make(map[string]Spec, len(entries))}

	for _, e := range entries {
		src, err := fs.ReadFile(builtinFS, path.Join("themes", e.Name()))
		if err != nil {
			panic(fmt.Sprintf("theme: reading embedded %s: %v", e.Name(), err))
		}

		name := strings.TrimSuffix(e.Name(), fileExt)

		spec, err := Parse(name, "builtin:"+e.Name(), src)
		if err != nil {
			panic(err.Error())
		}

		reg.specs[name] = spec
	}

	return reg
})

// Default is the embedded default theme, the base of every fallback chain.
func Default() Spec {
	return Builtins().specs[DefaultName]
}

// Load is the builtins plus every <dir>/<name>.theme. A missing directory is
// not an error. Dotfiles, subdirectories, and files without the .theme
// extension are ignored. A file that does not parse is skipped with a warning,
// and Lookup reports its error if it is ever selected — a broken file the
// operator is not using must not stop snatcher from starting.
//
// Theme files are operator-owned input, like config.json: symlinks are
// followed, and the directory is read as it is.
func Load(dir string) (Registry, error) {
	reg := Registry{specs: maps.Clone(Builtins().specs), broken: map[string]error{}}

	if dir == "" {
		return reg, nil
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return reg, nil
	}

	if err != nil {
		return Registry{}, fmt.Errorf("theme: reading %s: %w", dir, err)
	}

	for _, e := range entries {
		name, ok := themeStem(e.Name())
		if !ok {
			continue
		}

		file := filepath.Join(dir, e.Name())

		spec, err := loadFile(name, file)
		if errors.Is(err, errNotAFile) {
			continue
		}

		if err != nil {
			delete(reg.specs, name)
			reg.broken[name] = err
			reg.warnings = append(reg.warnings, fmt.Sprintf("skipping theme %q: %v", name, err))

			continue
		}

		reg.specs[name] = spec
	}

	return reg, nil
}

// errNotAFile marks a directory entry that is not a regular file once symlinks
// are followed, which Load ignores.
var errNotAFile = errors.New("not a regular file")

// themeStem is a directory entry's theme name, and whether it is a theme file at
// all.
func themeStem(entry string) (string, bool) {
	if strings.HasPrefix(entry, ".") || !strings.HasSuffix(entry, fileExt) {
		return "", false
	}

	return strings.TrimSuffix(entry, fileExt), true
}

// loadFile reads and parses one user theme file.
func loadFile(name, file string) (Spec, error) {
	info, err := os.Stat(file)
	if err != nil {
		return Spec{}, fmt.Errorf("%s: %w", file, err)
	}

	if !info.Mode().IsRegular() {
		return Spec{}, errNotAFile
	}

	if info.Size() > MaxFileSize {
		return Spec{}, fmt.Errorf("%s: theme file is larger than %d bytes", file, MaxFileSize)
	}

	src, err := os.ReadFile(file)
	if err != nil {
		return Spec{}, fmt.Errorf("%s: %w", file, err)
	}

	return Parse(name, file, src)
}

// Names is every usable theme name, sorted. A broken file's name is left out.
func (r Registry) Names() []string {
	return slices.Sorted(maps.Keys(r.specs))
}

// Lookup returns the named theme. Names match exactly, case included. A name
// no file answers to is ErrUnknown; a name whose user file did not parse is
// that file's parse error.
func (r Registry) Lookup(name string) (Spec, error) {
	if err, ok := r.broken[name]; ok {
		return Spec{}, err
	}

	if spec, ok := r.specs[name]; ok {
		return spec, nil
	}

	return Spec{}, fmt.Errorf("%w %q", ErrUnknown, name)
}

// Warnings describes each user file Load skipped.
func (r Registry) Warnings() []string {
	return slices.Clone(r.warnings)
}
