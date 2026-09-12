package verify

import (
	"debug/elf"
	"debug/macho"
	"runtime"
)

func validVyperExecutable(path string) bool {
	switch runtime.GOOS {
	case "linux":
		binary, err := elf.Open(path)
		if err != nil {
			return false
		}
		defer binary.Close() //nolint:errcheck
		return binary.Class == elf.ELFCLASS64 && ((runtime.GOARCH == "amd64" && binary.Machine == elf.EM_X86_64) || (runtime.GOARCH == "arm64" && binary.Machine == elf.EM_AARCH64))
	case "darwin":
		binary, err := macho.Open(path)
		if err != nil {
			return false
		}
		defer binary.Close() //nolint:errcheck
		return (runtime.GOARCH == "amd64" && binary.Cpu == macho.CpuAmd64) || (runtime.GOARCH == "arm64" && binary.Cpu == macho.CpuArm64)
	default:
		return false
	}
}
