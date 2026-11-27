package modules

import (
	"fmt"
	"runtime"
)

// The operating systems a program can be built for, as named by the
// cases of `os.Target` and by [c.<target>] sections of the project file.
var Targets = []string{"macos", "linux", "windows"}

// The system a program is built for.
type Target struct {
	// The operating system, one of [Targets].
	OS string

	// The processor architecture: arm64 or x64.
	Arch string
}

// Returns the system the compiler runs on, which is the one it builds for.
func Host() Target {
	t := Target{OS: "linux", Arch: "x64"}
	switch runtime.GOOS {
	case "darwin":
		t.OS = "macos"
	case "windows":
		t.OS = "windows"
	}
	if runtime.GOARCH == "arm64" {
		t.Arch = "arm64"
	}
	return t
}

// Returns the source of the constants of `vex:os` that name [t].
func (t Target) source() string {
	return fmt.Sprintf(`/// The operating system the program is built for. Branches that test it
/// for another system are checked but not compiled.
pub const target = Target.%s

/// The processor architecture the program is built for.
pub const arch = Arch.%s
`, t.OS, t.Arch)
}
