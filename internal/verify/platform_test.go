package verify

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutablePlatformReadsELFMachine(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		machine uint16
		want    string
	}{
		"amd64": {machine: 62, want: CompilerPlatformLinuxAMD64},
		"arm64": {machine: 183, want: CompilerPlatformLinuxARM64},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "compiler")
			if err := os.WriteFile(path, minimalELF(test.machine), 0o500); err != nil {
				t.Fatal(err)
			}
			got, err := executablePlatform(path)
			if err != nil || got != test.want {
				t.Fatalf("platform=%q error=%v, want %q", got, err, test.want)
			}
		})
	}
}

func TestExecutablePlatformReadsNativeFormats(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		content []byte
		want    string
	}{
		"mach-o-amd64": {
			content: minimalMachOAMD64(),
			want:    CompilerPlatformMacOSAMD64,
		},
		"pe-amd64": {
			content: minimalPEAMD64(),
			want:    CompilerPlatformWindowsAMD64,
		},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "compiler")
			if err := os.WriteFile(path, test.content, 0o500); err != nil {
				t.Fatal(err)
			}
			got, err := executablePlatform(path)
			if err != nil || got != test.want {
				t.Fatalf("platform=%q error=%v, want %q", got, err, test.want)
			}
		})
	}
}

func TestExecutablePlatformRejectsNonELF(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "compiler")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := executablePlatform(path); err == nil {
		t.Fatal("non-ELF compiler artifact was accepted")
	}
}

func TestExecutablePlatformRejectsNonStandaloneSolcJS(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "soljson.js")
	if err := os.WriteFile(path, []byte(`var Module = {};`), 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := executablePlatform(path); err == nil {
		t.Fatal("non-standalone solc-js package was accepted as an executable")
	}
}

func TestInspectRunnerPlatformUsesImageMetadata(t *testing.T) {
	t.Parallel()
	runtimePath := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\n" +
		"test \"$1 $2 $3\" = 'image inspect --format={{.Os}}/{{.Architecture}}' || exit 2\n" +
		"printf 'linux/arm64\\n'\n"
	if err := os.WriteFile(runtimePath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := inspectRunnerPlatform(
		context.Background(),
		runtimePath,
		"registry.example/runner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	)
	if err != nil || got != CompilerPlatformLinuxARM64 {
		t.Fatalf("platform=%q error=%v", got, err)
	}
}

func minimalELF(machine uint16) []byte {
	header := make([]byte, 64)
	copy(header[:4], []byte{0x7f, 'E', 'L', 'F'})
	header[4] = 2 // ELFCLASS64
	header[5] = 1 // ELFDATA2LSB
	header[6] = 1 // EV_CURRENT
	binary.LittleEndian.PutUint16(header[16:18], 2)
	binary.LittleEndian.PutUint16(header[18:20], machine)
	binary.LittleEndian.PutUint32(header[20:24], 1)
	binary.LittleEndian.PutUint16(header[52:54], 64)
	binary.LittleEndian.PutUint16(header[54:56], 56)
	binary.LittleEndian.PutUint16(header[58:60], 64)
	return header
}

func minimalMachOAMD64() []byte {
	header := make([]byte, 32)
	binary.LittleEndian.PutUint32(header[0:4], 0xfeedfacf)
	binary.LittleEndian.PutUint32(header[4:8], 0x01000007)
	binary.LittleEndian.PutUint32(header[8:12], 3)
	binary.LittleEndian.PutUint32(header[12:16], 2)
	return header
}

func minimalPEAMD64() []byte {
	header := make([]byte, 0x98)
	copy(header[0:2], "MZ")
	binary.LittleEndian.PutUint32(header[0x3c:0x40], 0x80)
	copy(header[0x80:0x84], "PE\x00\x00")
	binary.LittleEndian.PutUint16(header[0x84:0x86], 0x8664)
	return header
}
