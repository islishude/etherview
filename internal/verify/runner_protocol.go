package verify

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

var runnerFrameMagic = [8]byte{'E', 'V', 'V', 'R', 'U', 'N', '0', '2'}

const maxRunnerVersionBytes = 128

type RunnerFrame struct {
	Compiler []byte
	Input    []byte
	Version  string
	Digest   [sha256.Size]byte
}

func WriteRunnerFrame(writer io.Writer, frame RunnerFrame) error {
	if len(frame.Compiler) == 0 || len(frame.Compiler) > int(defaultCompilerArtifactMB) ||
		len(frame.Input) == 0 || len(frame.Input) > 64<<20 ||
		len(frame.Version) == 0 || len(frame.Version) > maxRunnerVersionBytes ||
		!versionPattern.MatchString(frame.Version) ||
		frame.Digest == [sha256.Size]byte{} ||
		sha256.Sum256(frame.Compiler) != frame.Digest {
		return errors.New("runner frame is invalid")
	}
	buffered := bufio.NewWriter(writer)
	if _, err := buffered.Write(runnerFrameMagic[:]); err != nil {
		return errors.New("write runner frame")
	}
	var lengths [18]byte
	binary.BigEndian.PutUint64(lengths[0:8], uint64(len(frame.Compiler)))
	binary.BigEndian.PutUint64(lengths[8:16], uint64(len(frame.Input)))
	binary.BigEndian.PutUint16(lengths[16:18], uint16(len(frame.Version)))
	if _, err := buffered.Write(lengths[:]); err != nil {
		return errors.New("write runner frame")
	}
	if _, err := buffered.Write(frame.Digest[:]); err != nil {
		return errors.New("write runner frame")
	}
	if _, err := buffered.WriteString(frame.Version); err != nil {
		return errors.New("write runner frame")
	}
	if _, err := buffered.Write(frame.Compiler); err != nil {
		return errors.New("write runner frame")
	}
	if _, err := buffered.Write(frame.Input); err != nil {
		return errors.New("write runner frame")
	}
	if err := buffered.Flush(); err != nil {
		return errors.New("flush runner frame")
	}
	return nil
}

func ReadRunnerFrame(reader io.Reader) (RunnerFrame, error) {
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || magic != runnerFrameMagic {
		return RunnerFrame{}, errors.New("runner frame magic is invalid")
	}
	var lengths [18]byte
	if _, err := io.ReadFull(reader, lengths[:]); err != nil {
		return RunnerFrame{}, errors.New("runner frame header is truncated")
	}
	compilerLength := binary.BigEndian.Uint64(lengths[0:8])
	inputLength := binary.BigEndian.Uint64(lengths[8:16])
	versionLength := binary.BigEndian.Uint16(lengths[16:18])
	if compilerLength == 0 || compilerLength > uint64(defaultCompilerArtifactMB) ||
		inputLength == 0 || inputLength > 64<<20 ||
		versionLength == 0 || versionLength > maxRunnerVersionBytes {
		return RunnerFrame{}, errors.New("runner frame lengths are invalid")
	}
	var frame RunnerFrame
	if _, err := io.ReadFull(reader, frame.Digest[:]); err != nil ||
		frame.Digest == [sha256.Size]byte{} {
		return RunnerFrame{}, errors.New("runner frame digest is invalid")
	}
	version := make([]byte, versionLength)
	if _, err := io.ReadFull(reader, version); err != nil {
		return RunnerFrame{}, errors.New("runner frame version is truncated")
	}
	frame.Version = string(version)
	if !versionPattern.MatchString(frame.Version) {
		return RunnerFrame{}, errors.New("runner frame version is invalid")
	}
	frame.Compiler = make([]byte, int(compilerLength))
	if _, err := io.ReadFull(reader, frame.Compiler); err != nil {
		return RunnerFrame{}, errors.New("runner frame compiler is truncated")
	}
	if sha256.Sum256(frame.Compiler) != frame.Digest {
		return RunnerFrame{}, errors.New("runner frame compiler checksum mismatch")
	}
	frame.Input = make([]byte, int(inputLength))
	if _, err := io.ReadFull(reader, frame.Input); err != nil {
		return RunnerFrame{}, errors.New("runner frame input is truncated")
	}
	var trailing [1]byte
	if count, err := reader.Read(trailing[:]); count != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return RunnerFrame{}, errors.New("runner frame contains trailing bytes")
	}
	return frame, nil
}
